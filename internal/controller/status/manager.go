package status

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	crdv1alpha1 "github.com/christensenjairus/Failover-Operator/api/v1alpha1"
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
	for _, volRepName := range policy.Spec.VolumeReplications {
		status := crdv1alpha1.VolumeReplicationStatus{
			Name:           volRepName,
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

// updateWorkloadStatus updates the status for workloads (deployments, statefulsets, cronjobs)
func (m *Manager) updateWorkloadStatus(ctx context.Context, policy *crdv1alpha1.FailoverPolicy) error {
	deployments := policy.Spec.Deployments
	statefulsets := policy.Spec.StatefulSets
	cronjobs := policy.Spec.CronJobs

	// If no workloads defined, update status accordingly
	if len(deployments) == 0 && len(statefulsets) == 0 && len(cronjobs) == 0 {
		policy.Status.WorkloadStatus = "No workloads defined"
		policy.Status.WorkloadStatuses = nil
		return nil
	}

	// Get detailed workload statuses
	workloadMgr := workload.NewManager(m.client)
	workloadStatuses := workloadMgr.GetWorkloadStatuses(ctx, policy.Namespace, deployments, statefulsets, cronjobs)
	policy.Status.WorkloadStatuses = workloadStatuses

	totalWorkloads := len(deployments) + len(statefulsets) + len(cronjobs)

	switch policy.Spec.DesiredState {
	case "primary":
		policy.Status.WorkloadStatus = fmt.Sprintf("%d workload(s) managed by Flux", totalWorkloads)
	case "secondary":
		scaledDownCount := 0
		suspendedCount := 0

		for _, status := range workloadStatuses {
			if status.Kind == "Deployment" || status.Kind == "StatefulSet" {
				if status.State == "Scaled Down" {
					scaledDownCount++
				}
			} else if status.Kind == "CronJob" {
				if status.State == "Suspended" {
					suspendedCount++
				}
			}
		}

		policy.Status.WorkloadStatus = fmt.Sprintf("%d/%d scaled down, %d/%d suspended",
			scaledDownCount,
			len(deployments)+len(statefulsets),
			suspendedCount,
			len(cronjobs))
	default:
		policy.Status.WorkloadStatus = "Workload state unknown"
	}

	return nil
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
