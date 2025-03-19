package status

import (
	"context"
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	crdv1alpha1 "github.com/christensenjairus/Failover-Operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/errors"
)

// Manager handles operations related to status updates
type Manager struct {
	client    client.Client
	vrManager VolumeReplicationStatusGetter
}

// VolumeReplicationStatusGetter is an interface for getting VolumeReplication status
type VolumeReplicationStatusGetter interface {
	GetCurrentVolumeReplicationState(ctx context.Context, name, namespace string) (string, error)
	CheckVolumeReplicationError(ctx context.Context, name, namespace string) (string, bool)
	GetTransitionMessage(currentSpec, currentStatus, desiredState string) string
	GetErrorMessage(ctx context.Context, name, namespace string) string
}

// NewManager creates a new Status manager
func NewManager(client client.Client, vrManager VolumeReplicationStatusGetter) *Manager {
	return &Manager{
		client:    client,
		vrManager: vrManager,
	}
}

// UpdateStatus updates the status of a FailoverPolicy
func (m *Manager) UpdateStatus(ctx context.Context, policy *crdv1alpha1.FailoverPolicy) error {
	log := log.FromContext(ctx)

	// Update the desired state status
	if err := m.updateDesiredStateStatus(ctx, policy); err != nil {
		log.Error(err, "Error updating desired state status")
		return err
	}

	// Update volume replication status
	if err := m.updateVolumeReplicationStatus(ctx, policy); err != nil {
		log.Error(err, "Error updating volume replication status")
		return err
	}

	// Update component health status
	m.updateHealthStatus(ctx, policy)

	return nil
}

// updateDesiredStateStatus updates the status based on the desired state annotation or spec field
func (m *Manager) updateDesiredStateStatus(ctx context.Context, policy *crdv1alpha1.FailoverPolicy) error {
	if annotation, ok := policy.Annotations["failover-operator.hahomelabs.com/desired-state"]; ok {
		// Handle values in the annotation (case-insensitive)
		switch strings.ToUpper(annotation) {
		case "PRIMARY", "ACTIVE":
			policy.Status.State = "PRIMARY"
		case "STANDBY", "SECONDARY", "PASSIVE":
			policy.Status.State = "STANDBY"
		default:
			// For unknown values, use uppercase
			policy.Status.State = strings.ToUpper(annotation)
		}
	} else if policy.Spec.DesiredState != "" {
		// Legacy field - map old terminology to new uppercase format
		switch strings.ToLower(policy.Spec.DesiredState) {
		case "active", "primary":
			policy.Status.State = "PRIMARY"
		case "passive", "secondary", "standby":
			policy.Status.State = "STANDBY"
		default:
			// For unknown values, use uppercase
			policy.Status.State = strings.ToUpper(policy.Spec.DesiredState)
		}
	} else {
		// Default to PRIMARY if nothing is specified
		policy.Status.State = "PRIMARY"
	}

	return nil
}

// updateVolumeReplicationStatus updates the status for volume replication resources
func (m *Manager) updateVolumeReplicationStatus(ctx context.Context, policy *crdv1alpha1.FailoverPolicy) error {
	// Set the current volume replication status count to 0
	policy.Status.VolumeReplicationStatuses = []crdv1alpha1.VolumeReplicationStatus{}

	// Group resources by type to get all volumeReplications
	resourcesByType := groupResourcesByType(policy)

	// If no volume replications defined, return early
	if len(resourcesByType.volumeReplications) == 0 {
		return nil
	}

	// Determine the desired state
	var desiredState string
	if val, exists := policy.Annotations["failover-operator.hahomelabs.com/desired-state"]; exists {
		desiredState = strings.ToUpper(val)
	} else if policy.Spec.DesiredState != "" {
		// Map legacy values to new format
		switch strings.ToLower(policy.Spec.DesiredState) {
		case "primary", "active":
			desiredState = "PRIMARY"
		case "secondary", "passive", "standby":
			desiredState = "STANDBY"
		default:
			desiredState = strings.ToUpper(policy.Spec.DesiredState)
		}
	} else {
		// Default to PRIMARY if not specified
		desiredState = "PRIMARY"
	}

	// For each volume replication, add a status entry
	for _, volRep := range resourcesByType.volumeReplications {
		namespace := volRep.Namespace
		if namespace == "" {
			namespace = policy.Namespace
		}

		// Actually get the current VolumeReplication state from the cluster
		state, err := m.vrManager.GetCurrentVolumeReplicationState(ctx, volRep.Name, namespace)
		if err != nil {
			// Handle the error
			status := crdv1alpha1.VolumeReplicationStatus{
				Name:           volRep.Name,
				State:          "unknown",
				Error:          err.Error(),
				LastUpdateTime: time.Now().Format(time.RFC3339),
				Message:        "Error getting volume replication state",
			}
			policy.Status.VolumeReplicationStatuses = append(policy.Status.VolumeReplicationStatuses, status)
			continue
		}

		// Check for any errors
		errMsg, hasError := m.vrManager.CheckVolumeReplicationError(ctx, volRep.Name, namespace)

		status := crdv1alpha1.VolumeReplicationStatus{
			Name:           volRep.Name,
			State:          state,
			LastUpdateTime: time.Now().Format(time.RFC3339),
		}

		// Handle different states based on the desired policy mode
		switch desiredState {
		case "PRIMARY":
			if hasError {
				status.Error = errMsg
				status.Message = "Volume replication error in primary mode"
			} else if !strings.Contains(state, "primary") {
				status.Message = "Volume is transitioning to primary mode"
			} else {
				status.Message = "Volume is in primary read-write mode"
			}
		case "STANDBY":
			if hasError {
				status.Error = errMsg
				status.Message = "Volume replication error in secondary mode"
			} else if !strings.Contains(state, "secondary") {
				status.Message = "Volume is transitioning to secondary mode"
			} else {
				status.Message = "Volume is in secondary read-only mode"
			}
		default:
			status.Message = "Volume replication state is unknown"
		}

		policy.Status.VolumeReplicationStatuses = append(policy.Status.VolumeReplicationStatuses, status)
	}

	return nil
}

// updateHealthStatus updates the health status of the policy and its components
func (m *Manager) updateHealthStatus(ctx context.Context, policy *crdv1alpha1.FailoverPolicy) {
	log := log.FromContext(ctx)

	// Initialize overall health as OK, will be updated based on component health
	policy.Status.Health = "OK"

	// Clear existing components status
	policy.Status.Components = []crdv1alpha1.ComponentStatus{}

	// Check if there are components defined
	if len(policy.Spec.Components) == 0 {
		policy.Status.Health = "OK" // Default to OK if no components
		log.Info("No components defined in spec, setting default health", "health", policy.Status.Health)
		return
	}

	// Maps for counting component health levels
	healthCounts := map[string]int{
		"OK":       0,
		"DEGRADED": 0,
		"ERROR":    0,
	}

	// Check health for each component in spec
	for _, component := range policy.Spec.Components {
		compStatus := crdv1alpha1.ComponentStatus{
			Name:   component.Name,
			Health: "OK", // Default to OK, will be updated based on checks
		}

		// Get deployments and statefulsets from component workloads
		var deployments, statefulSets []string
		for _, workload := range component.Workloads {
			if workload.Kind == "Deployment" {
				deployments = append(deployments, workload.Name)
			} else if workload.Kind == "StatefulSet" {
				statefulSets = append(statefulSets, workload.Name)
			}
		}

		// Check deployments health
		for _, deployName := range deployments {
			// Get the deployment
			deployment := &appsv1.Deployment{}
			if err := m.client.Get(ctx, types.NamespacedName{Name: deployName, Namespace: policy.Namespace}, deployment); err != nil {
				if !errors.IsNotFound(err) {
					log.Error(err, "Failed to get Deployment", "name", deployName, "namespace", policy.Namespace)
				}
				compStatus.Health = "ERROR"
				compStatus.Message = "Deployment not found or error retrieving it"
				break
			}

			// Check if deployment is healthy (all pods available and ready)
			if deployment.Status.ReadyReplicas < deployment.Status.Replicas ||
				deployment.Status.AvailableReplicas < deployment.Status.Replicas {

				// If desired state is STANDBY, we expect scaled down resources
				if policy.Status.State == "STANDBY" && deployment.Spec.Replicas != nil && *deployment.Spec.Replicas == 0 {
					// This is expected in STANDBY mode
					continue
				}

				if deployment.Status.ReadyReplicas == 0 {
					compStatus.Health = "ERROR"
					compStatus.Message = "No ready pods in deployment"
				} else {
					compStatus.Health = "DEGRADED"
					compStatus.Message = "Deployment has unhealthy pods"
				}
				break
			}
		}

		// Check StatefulSets health if deployment checks passed
		if compStatus.Health == "OK" {
			for _, stsName := range statefulSets {
				// Get the statefulset
				statefulSet := &appsv1.StatefulSet{}
				if err := m.client.Get(ctx, types.NamespacedName{Name: stsName, Namespace: policy.Namespace}, statefulSet); err != nil {
					if !errors.IsNotFound(err) {
						log.Error(err, "Failed to get StatefulSet", "name", stsName, "namespace", policy.Namespace)
					}
					compStatus.Health = "ERROR"
					compStatus.Message = "StatefulSet not found or error retrieving it"
					break
				}

				// Check if statefulset is healthy (all pods ready)
				if statefulSet.Status.ReadyReplicas < statefulSet.Status.Replicas {
					// If desired state is STANDBY, we expect scaled down resources
					if policy.Status.State == "STANDBY" && statefulSet.Spec.Replicas != nil && *statefulSet.Spec.Replicas == 0 {
						// This is expected in STANDBY mode
						continue
					}

					if statefulSet.Status.ReadyReplicas == 0 {
						compStatus.Health = "ERROR"
						compStatus.Message = "No ready pods in statefulset"
					} else {
						compStatus.Health = "DEGRADED"
						compStatus.Message = "StatefulSet has unhealthy pods"
					}
					break
				}
			}
		}

		// Check VolumeReplications health if other checks passed
		if compStatus.Health == "OK" && len(component.VolumeReplications) > 0 {
			for _, volRepName := range component.VolumeReplications {
				// Check the VolumeReplication state using the interface method
				state, err := m.vrManager.GetCurrentVolumeReplicationState(ctx, volRepName, policy.Namespace)
				if err != nil {
					log.Error(err, "Failed to get VolumeReplication state", "name", volRepName, "namespace", policy.Namespace)
					compStatus.Health = "ERROR"
					compStatus.Message = "Failed to get VolumeReplication state"
					break
				}

				// Check for errors
				errMsg, hasError := m.vrManager.CheckVolumeReplicationError(ctx, volRepName, policy.Namespace)
				if hasError {
					compStatus.Health = "ERROR"
					compStatus.Message = "VolumeReplication error: " + errMsg
					break
				}

				// Validate state matches expected state based on policy mode
				if policy.Status.State == "PRIMARY" && !strings.Contains(state, "primary") {
					compStatus.Health = "DEGRADED"
					compStatus.Message = "VolumeReplication not in primary state when policy is PRIMARY"
				} else if policy.Status.State == "STANDBY" && !strings.Contains(state, "secondary") {
					compStatus.Health = "DEGRADED"
					compStatus.Message = "VolumeReplication not in secondary state when policy is STANDBY"
				}
			}
		}

		// Add to status
		policy.Status.Components = append(policy.Status.Components, compStatus)
		healthCounts[compStatus.Health]++
	}

	// Update overall health based on component health counts
	// If any component is ERROR, the overall health is ERROR
	// If any component is DEGRADED (and none are ERROR), the overall health is DEGRADED
	// Otherwise, the overall health is OK
	if healthCounts["ERROR"] > 0 {
		policy.Status.Health = "ERROR"
	} else if healthCounts["DEGRADED"] > 0 {
		policy.Status.Health = "DEGRADED"
	} else {
		policy.Status.Health = "OK"
	}

	log.Info("Health status updated",
		"policy", policy.Name,
		"health", policy.Status.Health,
		"components", len(policy.Status.Components),
		"ok", healthCounts["OK"],
		"degraded", healthCounts["DEGRADED"],
		"error", healthCounts["ERROR"])
}

// ResourcesByType holds references to resources grouped by their type
type ResourcesByType struct {
	volumeReplications []crdv1alpha1.ResourceReference
	virtualServices    []crdv1alpha1.ResourceReference
	deployments        []crdv1alpha1.ResourceReference
	statefulSets       []crdv1alpha1.ResourceReference
	cronJobs           []crdv1alpha1.ResourceReference
	helmReleases       []crdv1alpha1.ResourceReference
	kustomizations     []crdv1alpha1.ResourceReference
}

// groupResourcesByType organizes resources from managedResources by their type
// It also handles legacy resource references for backward compatibility
func groupResourcesByType(policy *crdv1alpha1.FailoverPolicy) ResourcesByType {
	result := ResourcesByType{
		volumeReplications: make([]crdv1alpha1.ResourceReference, 0),
		virtualServices:    make([]crdv1alpha1.ResourceReference, 0),
		deployments:        make([]crdv1alpha1.ResourceReference, 0),
		statefulSets:       make([]crdv1alpha1.ResourceReference, 0),
		cronJobs:           make([]crdv1alpha1.ResourceReference, 0),
		helmReleases:       make([]crdv1alpha1.ResourceReference, 0),
		kustomizations:     make([]crdv1alpha1.ResourceReference, 0),
	}

	// Add resources from managedResources field
	for _, resource := range policy.Spec.ManagedResources {
		ref := crdv1alpha1.ResourceReference{
			Name:      resource.Name,
			Namespace: resource.Namespace,
		}

		// Use the Kind field from ManagedResource
		switch resource.Kind {
		case "VolumeReplication":
			result.volumeReplications = append(result.volumeReplications, ref)
		case "VirtualService":
			result.virtualServices = append(result.virtualServices, ref)
		case "Deployment":
			result.deployments = append(result.deployments, ref)
		case "StatefulSet":
			result.statefulSets = append(result.statefulSets, ref)
		case "CronJob":
			result.cronJobs = append(result.cronJobs, ref)
		case "HelmRelease":
			result.helmReleases = append(result.helmReleases, ref)
		case "Kustomization":
			result.kustomizations = append(result.kustomizations, ref)
		}
	}

	// Handle legacy resource references for backward compatibility
	for _, vr := range policy.Spec.VolumeReplications {
		result.volumeReplications = append(result.volumeReplications, vr)
	}
	for _, vs := range policy.Spec.VirtualServices {
		result.virtualServices = append(result.virtualServices, vs)
	}
	for _, deploy := range policy.Spec.Deployments {
		result.deployments = append(result.deployments, deploy)
	}
	for _, sts := range policy.Spec.StatefulSets {
		result.statefulSets = append(result.statefulSets, sts)
	}
	for _, cj := range policy.Spec.CronJobs {
		result.cronJobs = append(result.cronJobs, cj)
	}
	for _, hr := range policy.Spec.HelmReleases {
		result.helmReleases = append(result.helmReleases, hr)
	}
	for _, k := range policy.Spec.Kustomizations {
		result.kustomizations = append(result.kustomizations, k)
	}

	return result
}

// getResourceNames extracts names from a slice of ResourceReference
func getResourceNames(refs []crdv1alpha1.ResourceReference) []string {
	names := make([]string, len(refs))
	for i, ref := range refs {
		names[i] = ref.Name
	}
	return names
}

// UpdateFailoverStatus updates the status of a FailoverPolicy
func (m *Manager) UpdateFailoverStatus(ctx context.Context, failoverPolicy *crdv1alpha1.FailoverPolicy, failoverError bool, errorMessage string) error {
	// Get desired state from annotations (preferred) or spec (legacy)
	var desiredState string
	if val, exists := failoverPolicy.Annotations["failover-operator.hahomelabs.com/desired-state"]; exists {
		// Normalize the annotation value to uppercase for the new format
		desiredState = strings.ToUpper(val)
	} else if failoverPolicy.Spec.DesiredState != "" {
		// Legacy support for spec.desiredState - map to new uppercase format
		switch strings.ToLower(failoverPolicy.Spec.DesiredState) {
		case "primary", "active":
			desiredState = "PRIMARY"
		case "secondary", "passive", "standby":
			desiredState = "STANDBY"
		default:
			desiredState = strings.ToUpper(failoverPolicy.Spec.DesiredState)
		}
	} else {
		// Default to PRIMARY if not specified
		desiredState = "PRIMARY"
	}

	// Update the state of the failover policy
	if !failoverError {
		failoverPolicy.Status.State = desiredState
	}

	// Update health status
	m.updateHealthStatus(ctx, failoverPolicy)

	if failoverError {
		meta.SetStatusCondition(&failoverPolicy.Status.Conditions, metav1.Condition{
			Type:               "FailoverComplete",
			Status:             metav1.ConditionFalse,
			Reason:             "FailoverError",
			Message:            errorMessage,
			LastTransitionTime: metav1.Now(),
		})
	} else {
		meta.SetStatusCondition(&failoverPolicy.Status.Conditions, metav1.Condition{
			Type:               "FailoverComplete",
			Status:             metav1.ConditionTrue,
			Reason:             "FailoverComplete",
			Message:            fmt.Sprintf("Failover to %s mode completed successfully", desiredState),
			LastTransitionTime: metav1.Now(),
		})
	}

	return nil
}
