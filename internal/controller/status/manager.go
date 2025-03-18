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
	"github.com/christensenjairus/Failover-Operator/internal/controller/flux"
	"github.com/christensenjairus/Failover-Operator/internal/controller/workload"
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

// UpdateStatus updates the failover policy's status based on the current state
func (m *Manager) UpdateStatus(ctx context.Context, policy *crdv1alpha1.FailoverPolicy) error {
	log := log.FromContext(ctx)

	// Update the FailoverPolicy status
	if err := m.updateDesiredStateStatus(ctx, policy); err != nil {
		return err
	}

	if err := m.updateVolumeReplicationStatus(ctx, policy); err != nil {
		return err
	}

	if err := m.updateWorkloadStatus(ctx, policy); err != nil {
		return err
	}

	log.Info("Status updated", "Name", policy.Name, "Namespace", policy.Namespace)
	return nil
}

// updateDesiredStateStatus updates the status message based on the desired state
func (m *Manager) updateDesiredStateStatus(ctx context.Context, policy *crdv1alpha1.FailoverPolicy) error {
	switch policy.Spec.DesiredState {
	case "primary":
		policy.Status.CurrentState = "Primary"
	case "secondary":
		policy.Status.CurrentState = "Secondary"
	default:
		policy.Status.CurrentState = "Unknown"
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

// updateWorkloadStatus updates the status for workloads (deployments, statefulsets, cronjobs, flux resources)
func (m *Manager) updateWorkloadStatus(ctx context.Context, policy *crdv1alpha1.FailoverPolicy) error {
	// Group resources by type for both legacy fields and the new managedResources field
	resourcesByType := groupResourcesByType(policy)

	// Get resource counts
	deploymentCount := len(resourcesByType.deployments)
	statefulsetCount := len(resourcesByType.statefulSets)
	cronjobCount := len(resourcesByType.cronJobs)
	fluxCount := len(resourcesByType.helmReleases) + len(resourcesByType.kustomizations)

	// If no workloads or flux resources defined, update status accordingly
	if deploymentCount == 0 && statefulsetCount == 0 && cronjobCount == 0 && fluxCount == 0 {
		policy.Status.WorkloadStatus = "No resources defined"
		policy.Status.WorkloadStatuses = nil
		return nil
	}

	// Handle primary or secondary mode differently
	switch policy.Spec.DesiredState {
	case "primary":
		// In primary mode, clear detailed workload statuses and just show summary
		totalWorkloads := deploymentCount + statefulsetCount + cronjobCount

		var parts []string
		if totalWorkloads > 0 {
			parts = append(parts, fmt.Sprintf("%d workload(s) managed by Flux", totalWorkloads))
		}

		if fluxCount > 0 {
			parts = append(parts, fmt.Sprintf("%d Flux resource(s) active", fluxCount))
		}

		policy.Status.WorkloadStatus = strings.Join(parts, ", ")
		policy.Status.WorkloadStatuses = nil // Clear detailed statuses in primary mode

	case "secondary":
		// In secondary mode, collect detailed workload statuses
		var allStatuses []crdv1alpha1.WorkloadStatus

		// Get Kubernetes workload statuses if defined
		if deploymentCount > 0 || statefulsetCount > 0 || cronjobCount > 0 {
			// Convert to name-only lists for backward compatibility with workload manager
			deploymentNames := getResourceNames(resourcesByType.deployments)
			statefulsetNames := getResourceNames(resourcesByType.statefulSets)
			cronjobNames := getResourceNames(resourcesByType.cronJobs)

			workloadMgr := workload.NewManager(m.client)
			k8sStatuses := workloadMgr.GetWorkloadStatuses(ctx, policy.Namespace,
				deploymentNames, statefulsetNames, cronjobNames)
			allStatuses = append(allStatuses, k8sStatuses...)
		}

		// Get Flux resource statuses if defined
		if fluxCount > 0 {
			fluxMgr := flux.NewManager(m.client)
			fluxStatuses := fluxMgr.GetFluxStatuses(ctx,
				resourcesByType.helmReleases, resourcesByType.kustomizations, policy.Namespace)
			allStatuses = append(allStatuses, fluxStatuses...)
		}

		// Update the status with the combined workload statuses
		policy.Status.WorkloadStatuses = allStatuses

		// Calculate summary statistics
		scaledDownCount := 0
		suspendedCronJobCount := 0
		suspendedFluxCount := 0

		for _, status := range allStatuses {
			switch status.Kind {
			case "Deployment", "StatefulSet":
				if status.State == "Scaled Down" {
					scaledDownCount++
				}
			case "CronJob":
				if status.State == "Suspended" {
					suspendedCronJobCount++
				}
			case "HelmRelease", "Kustomization":
				if status.State == "Suspended" {
					suspendedFluxCount++
				}
			}
		}

		// Create the status message parts
		var parts []string

		if deploymentCount+statefulsetCount > 0 {
			parts = append(parts, fmt.Sprintf("%d/%d scaled down",
				scaledDownCount, deploymentCount+statefulsetCount))
		}

		if cronjobCount > 0 {
			parts = append(parts, fmt.Sprintf("%d/%d suspended",
				suspendedCronJobCount, cronjobCount))
		}

		if fluxCount > 0 {
			parts = append(parts, fmt.Sprintf("%d/%d Flux resources suspended",
				suspendedFluxCount, fluxCount))
		}

		policy.Status.WorkloadStatus = strings.Join(parts, ", ")

	default:
		policy.Status.WorkloadStatus = "Resource state unknown"
	}

	return nil
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

	// Update current state
	failoverPolicy.Status.CurrentState = failoverPolicy.Spec.DesiredState

	// Update workload status
	m.updateWorkloadStatus(ctx, failoverPolicy)

	if failoverError {
		// Create error condition
		condition := metav1.Condition{
			Type:               "FailoverReady",
			Status:             metav1.ConditionFalse,
			Reason:             "FailoverError",
			Message:            errorMessage,
			LastTransitionTime: metav1.Now(),
		}
		meta.SetStatusCondition(&failoverPolicy.Status.Conditions, condition)
	} else {
		// Create success condition
		condition := metav1.Condition{
			Type:               "FailoverReady",
			Status:             metav1.ConditionTrue,
			Reason:             "FailoverComplete",
			Message:            fmt.Sprintf("Failover to %s mode completed successfully", failoverPolicy.Spec.DesiredState),
			LastTransitionTime: metav1.Now(),
		}
		meta.SetStatusCondition(&failoverPolicy.Status.Conditions, condition)
	}

	if err := m.client.Status().Update(ctx, failoverPolicy); err != nil {
		log.Error(err, "Failed to update FailoverPolicy status")
		return err
	}

	return nil
}
