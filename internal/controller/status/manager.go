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

// UpdateFailoverStatus updates the status of a FailoverPolicy
func (m *Manager) UpdateFailoverStatus(ctx context.Context, failoverPolicy *crdv1alpha1.FailoverPolicy, pendingUpdates int, failoverError bool, failoverErrorMessage string) error {
	log := log.FromContext(ctx)

	// Only track important VolumeReplications: those with errors or not yet in desired state
	var statuses []crdv1alpha1.VolumeReplicationStatus
	for _, vrName := range failoverPolicy.Spec.VolumeReplications {
		currentState, err := m.vrManager.GetCurrentVolumeReplicationState(ctx, vrName, failoverPolicy.Namespace)
		if err != nil {
			// Include in status if we can't get the state
			statuses = append(statuses, crdv1alpha1.VolumeReplicationStatus{
				Name:           vrName,
				Error:          fmt.Sprintf("Failed to get state: %v", err),
				LastUpdateTime: time.Now().Format(time.RFC3339),
			})
			continue
		}

		// Check for errors
		if errorMsg, hasError := m.vrManager.CheckVolumeReplicationError(ctx, vrName, failoverPolicy.Namespace); hasError {
			// Add to status with error
			statuses = append(statuses, crdv1alpha1.VolumeReplicationStatus{
				Name:           vrName,
				State:          currentState,
				Error:          errorMsg,
				LastUpdateTime: time.Now().Format(time.RFC3339),
			})
			continue
		}

		desiredState := strings.ToLower(failoverPolicy.Spec.DesiredState)

		// Get message based on state
		var statusMsg string

		// First check for transition messages
		transitionMsg := m.vrManager.GetTransitionMessage(desiredState, currentState, desiredState)
		if transitionMsg != "" {
			statusMsg = transitionMsg
		} else {
			// Then check for specific error messages
			errorMsg := m.vrManager.GetErrorMessage(ctx, vrName, failoverPolicy.Namespace)
			if errorMsg != "" {
				statusMsg = errorMsg
			}
		}

		// Only include volumes not yet in desired state or with important messages
		if !strings.EqualFold(currentState, desiredState) || statusMsg != "" {
			status := crdv1alpha1.VolumeReplicationStatus{
				Name:           vrName,
				State:          currentState,
				Message:        statusMsg,
				LastUpdateTime: time.Now().Format(time.RFC3339),
			}
			statuses = append(statuses, status)
		}
	}

	// Update status with only the important VolumeReplications
	failoverPolicy.Status.VolumeReplicationStatuses = statuses

	if failoverError {
		meta.SetStatusCondition(&failoverPolicy.Status.Conditions, metav1.Condition{
			Type:    "FailoverError",
			Status:  metav1.ConditionTrue,
			Reason:  "VolumeReplicationError",
			Message: failoverErrorMessage,
		})

		meta.RemoveStatusCondition(&failoverPolicy.Status.Conditions, "FailoverComplete")
		meta.RemoveStatusCondition(&failoverPolicy.Status.Conditions, "FailoverInProgress")
	} else {
		meta.RemoveStatusCondition(&failoverPolicy.Status.Conditions, "FailoverError")
	}

	failoverComplete := (pendingUpdates == 0)
	failoverPolicy.Status.PendingVolumeReplicationUpdates = pendingUpdates

	if failoverComplete {
		failoverPolicy.Status.CurrentState = failoverPolicy.Spec.DesiredState
		meta.SetStatusCondition(&failoverPolicy.Status.Conditions, metav1.Condition{
			Type:    "FailoverComplete",
			Status:  metav1.ConditionTrue,
			Reason:  "AllReplicationsSynced",
			Message: "All VolumeReplication objects have been updated successfully.",
		})

		meta.RemoveStatusCondition(&failoverPolicy.Status.Conditions, "FailoverInProgress")
	} else {
		meta.SetStatusCondition(&failoverPolicy.Status.Conditions, metav1.Condition{
			Type:    "FailoverInProgress",
			Status:  metav1.ConditionTrue,
			Reason:  "SyncingReplications",
			Message: fmt.Sprintf("%d VolumeReplication objects are still syncing.", pendingUpdates),
		})

		meta.RemoveStatusCondition(&failoverPolicy.Status.Conditions, "FailoverComplete")
	}

	if err := m.client.Status().Update(ctx, failoverPolicy); err != nil {
		log.Error(err, "Failed to update FailoverPolicy status")
		return err
	}

	log.Info("Updated FailoverPolicy status", "FailoverPolicy", failoverPolicy.Name)
	return nil
}
