package status

import (
	"context"
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	crdv1alpha1 "github.com/christensenjairus/Failover-Operator/api/v1alpha1"
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

// updateDesiredStateStatus updates the status message based on the desired state
func (m *Manager) updateDesiredStateStatus(ctx context.Context, policy *crdv1alpha1.FailoverPolicy) error {
	if annotation, ok := policy.Annotations["failover-operator.hahomelabs.com/desired-state"]; ok {
		// Normalize annotation value
		desiredAnno := strings.ToLower(annotation)

		// Handle both new and old terminology, including 'inactive'
		if desiredAnno == "active" || desiredAnno == "primary" {
			policy.Status.State = "PRIMARY"
		} else if desiredAnno == "passive" || desiredAnno == "secondary" || desiredAnno == "inactive" || desiredAnno == "standby" {
			policy.Status.State = "STANDBY"
		} else {
			policy.Status.State = "Unknown"
		}
	} else if policy.Spec.DesiredState != "" {
		// Legacy field - map old terminology to new
		specState := strings.ToLower(policy.Spec.DesiredState)
		if specState == "active" || specState == "primary" {
			policy.Status.State = "PRIMARY"
		} else if specState == "passive" || specState == "secondary" || specState == "standby" {
			policy.Status.State = "STANDBY"
		} else {
			policy.Status.State = "Unknown"
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

	// If no volume replications defined, clear the statuses
	if len(policy.Spec.VolumeReplications) == 0 {
		return nil
	}

	// For each volume replication, add a status entry
	for _, volRep := range policy.Spec.VolumeReplications {
		status := crdv1alpha1.VolumeReplicationStatus{
			Name:           volRep.Name,
			LastUpdateTime: time.Now().Format(time.RFC3339),
		}

		switch policy.Spec.DesiredState {
		case "primary":
			status.State = "primary-rwx"
			status.Message = "Volume is in primary read-write mode"
		case "secondary":
			status.State = "secondary-ro"
			status.Message = "Volume is in secondary read-only mode"
		default:
			status.State = "unknown"
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

	// Check health for each component in spec
	for _, component := range policy.Spec.Components {
		compStatus := crdv1alpha1.ComponentStatus{
			Name:   component.Name,
			Health: "OK", // Default to OK
		}

		// Various health checks would go here
		// For now, just add the component with OK status

		// Add to status
		policy.Status.Components = append(policy.Status.Components, compStatus)
	}

	log.Info("Health status updated", "policy", policy.Name, "health", policy.Status.Health)
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
	log := log.FromContext(ctx)

	// Get desired state from annotations (preferred) or spec (legacy)
	var desiredState string
	if val, exists := failoverPolicy.Annotations["failover-operator.hahomelabs.com/desired-state"]; exists {
		// Normalize the annotation value
		annoVal := strings.ToLower(val)

		if annoVal == "primary" || annoVal == "active" {
			desiredState = "active"
		} else if annoVal == "standby" || annoVal == "secondary" || annoVal == "passive" {
			desiredState = "passive"
		} else {
			log.Info("Unknown desired state in annotation", "value", val)
			desiredState = annoVal
		}
	} else if failoverPolicy.Spec.DesiredState != "" {
		// Legacy support for spec.desiredState
		specState := strings.ToLower(failoverPolicy.Spec.DesiredState)
		if specState == "primary" {
			desiredState = "active"
		} else if specState == "secondary" || specState == "standby" {
			desiredState = "passive"
		} else {
			desiredState = specState
		}
	} else {
		// Default to active if not specified
		desiredState = "active"
	}

	// Update the state of the failover policy
	if !failoverError {
		if desiredState == "active" {
			failoverPolicy.Status.State = "PRIMARY"
		} else {
			failoverPolicy.Status.State = "STANDBY"
		}
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
