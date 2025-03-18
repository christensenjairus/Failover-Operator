package volumereplication

import (
	"context"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	replicationv1alpha1 "github.com/csi-addons/kubernetes-csi-addons/api/replication.storage/v1alpha1"
)

// Manager handles operations related to VolumeReplication resources
type Manager struct {
	client client.Client
}

// NewManager creates a new VolumeReplication manager
func NewManager(client client.Client) *Manager {
	return &Manager{
		client: client,
	}
}

// UpdateVolumeReplication updates a VolumeReplication resource to the desired state
func (m *Manager) UpdateVolumeReplication(ctx context.Context, name, namespace, state string) (bool, error) {
	log := log.FromContext(ctx)
	volumeReplication := &replicationv1alpha1.VolumeReplication{}

	// Fetch the VolumeReplication object
	err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, volumeReplication)
	if err != nil {
		log.Error(err, "Failed to get VolumeReplication", "VolumeReplication", name)
		return false, client.IgnoreNotFound(err)
	}

	currentSpec := string(volumeReplication.Spec.ReplicationState)
	currentStatus := strings.ToLower(string(volumeReplication.Status.State))
	desiredState := strings.ToLower(state)

	log.Info("Checking VolumeReplication state",
		"name", name,
		"currentSpec", currentSpec,
		"currentStatus", currentStatus,
		"desiredState", desiredState)

	// If in resync mode, wait for completion
	if currentStatus == "resync" {
		log.Info("VolumeReplication is in resync mode - waiting for completion",
			"name", name)
		return true, nil
	}

	// Handle primary->secondary transition specially
	if currentStatus == "primary" && desiredState == "secondary" {
		if currentSpec != state {
			log.Info("Initiating primary->secondary transition - degraded state expected temporarily",
				"name", name)
			volumeReplication.Spec.ReplicationState = replicationv1alpha1.ReplicationState(state)
			if err := m.client.Update(ctx, volumeReplication); err != nil {
				log.Error(err, "Failed to update VolumeReplication", "VolumeReplication", name)
				return true, err
			}
		}
		return true, nil // Always indicate we're pending for primary->secondary transitions
	}

	// Check if volume is in an error state but it's actually transitioning
	if currentStatus == "error" {
		// Look for degraded condition to check if it's a normal transition
		for _, condition := range volumeReplication.Status.Conditions {
			if condition.Type == "Degraded" && condition.Status == metav1.ConditionTrue {
				if condition.Reason == "VolumeDegraded" &&
					(strings.Contains(condition.Message, "volume is degraded") ||
						strings.Contains(condition.Message, "state transition")) {
					// This is likely a transitional state, proceed with the update
					log.Info("Volume shows error but appears to be in transition, proceeding",
						"name", name,
						"reason", condition.Reason,
						"message", condition.Message)

					// Only update if needed
					if currentSpec != state {
						volumeReplication.Spec.ReplicationState = replicationv1alpha1.ReplicationState(state)
						if err := m.client.Update(ctx, volumeReplication); err != nil {
							log.Error(err, "Failed to update VolumeReplication during transition", "VolumeReplication", name)
							return true, err
						}
					}
					return true, nil
				}
			}
		}
	}

	// If transitioning to primary, ensure current status is either secondary or already primary
	if desiredState == "primary" && currentStatus != "secondary" && currentStatus != "primary" {
		log.Info("Cannot transition to primary - current status must be secondary or primary",
			"name", name,
			"currentStatus", currentStatus)
		return true, nil
	}

	// Only update spec if it doesn't match desired state and status is stable
	if currentSpec != state {
		// Check if status is in a stable state before updating
		if currentStatus == "error" {
			log.Info("Cannot update spec - VolumeReplication is in error state",
				"name", name)
			return true, nil
		}

		log.Info("Updating VolumeReplication spec",
			"name", name,
			"currentSpec", currentSpec,
			"desiredState", state)

		volumeReplication.Spec.ReplicationState = replicationv1alpha1.ReplicationState(state)
		if err := m.client.Update(ctx, volumeReplication); err != nil {
			log.Error(err, "Failed to update VolumeReplication", "VolumeReplication", name)
			return true, err
		}
		return true, nil
	}

	// Check if status matches spec
	if currentStatus != desiredState {
		log.Info("VolumeReplication status not yet matching spec",
			"name", name,
			"statusState", currentStatus,
			"desiredState", desiredState)
		return true, nil
	}

	return false, nil
}

// CheckVolumeReplicationError checks if a VolumeReplication resource has errors
func (m *Manager) CheckVolumeReplicationError(ctx context.Context, name, namespace string) (string, bool) {
	log := log.FromContext(ctx)
	volumeReplication := &replicationv1alpha1.VolumeReplication{}

	// Fetch the VolumeReplication object
	err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, volumeReplication)
	if err != nil {
		log.Error(err, "Failed to get VolumeReplication", "VolumeReplication", name)
		return "", false
	}

	// Get the current spec and status state
	currentSpec := string(volumeReplication.Spec.ReplicationState)
	currentStatus := strings.ToLower(string(volumeReplication.Status.State))

	// Scan conditions for errors
	for _, condition := range volumeReplication.Status.Conditions {
		if condition.Type == "Degraded" && condition.Status == metav1.ConditionTrue {
			// Check if we're in a known transitional state
			isTransitioning := false
			isExpectedError := false

			// Check for specific error messages that are expected and should be handled gracefully
			if condition.Reason == "Error" && strings.Contains(condition.Message, "last sync time not found") {
				log.Info("VolumeReplication has expected 'last sync time not found' error - this is normal for new volumes",
					"name", name,
					"reason", condition.Reason,
					"message", condition.Message)
				isExpectedError = true
			}

			// Check for resync errors which might be temporary
			if strings.Contains(condition.Message, "failed to resync") {
				log.Info("VolumeReplication has resync error - this may be temporary",
					"name", name,
					"reason", condition.Reason,
					"message", condition.Message)
				isExpectedError = true
			}

			// Primary -> Secondary transition: always expect temporary degraded state
			if currentSpec == "secondary" && (currentStatus == "primary" || currentStatus == "resync") {
				isTransitioning = true
			}

			// After a spec update but before status reflects it, we're in transition
			if currentSpec != currentStatus && currentStatus != "error" {
				isTransitioning = true
			}

			// During resync operations
			if currentStatus == "resync" {
				isTransitioning = true
			}

			// If spec was changed very recently (status might not have caught up)
			if condition.Reason == "VolumeDegraded" &&
				(strings.Contains(condition.Message, "volume is degraded") ||
					strings.Contains(condition.Message, "state transition")) {
				isTransitioning = true
			}

			if isTransitioning || isExpectedError {
				msgPrefix := "VolumeReplication is in expected transitional state"
				if isExpectedError {
					msgPrefix = "VolumeReplication has expected error"
				}

				log.Info(msgPrefix,
					"name", name,
					"reason", condition.Reason,
					"message", condition.Message,
					"currentSpec", currentSpec,
					"currentStatus", currentStatus)
				return "", false // Don't treat as error
			}

			// Otherwise, report the error
			log.Error(fmt.Errorf("VolumeReplication degraded"), "VolumeReplication has an error",
				"name", name,
				"reason", condition.Reason,
				"message", condition.Message,
				"currentSpec", currentSpec,
				"currentStatus", currentStatus)
			return condition.Message, true
		}
	}

	return "", false // No errors found
}

// GetCurrentVolumeReplicationState retrieves the current state of a VolumeReplication resource
func (m *Manager) GetCurrentVolumeReplicationState(ctx context.Context, name, namespace string) (string, error) {
	volumeReplication := &replicationv1alpha1.VolumeReplication{}

	err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, volumeReplication)
	if err != nil {
		return "", err
	}

	// Convert to lowercase to ensure consistent comparison
	return strings.ToLower(string(volumeReplication.Status.State)), nil
}

// isTransitionalState checks if the VolumeReplication is in a transitional state
func (m *Manager) isTransitionalState(currentSpec, currentStatus, desiredState string) bool {
	// Primary -> Secondary transition
	if (currentStatus == "primary" || currentStatus == "resync") &&
		currentSpec == "secondary" &&
		desiredState == "secondary" {
		return true
	}

	// Secondary -> Primary transition
	if (currentStatus == "secondary" || currentStatus == "resync") &&
		currentSpec == "primary" &&
		desiredState == "primary" {
		return true
	}

	// Any resync state is transitional
	if currentStatus == "resync" {
		return true
	}

	return false
}

// GetTransitionMessage returns an appropriate message for a volume in transition
func (m *Manager) GetTransitionMessage(currentSpec, currentStatus, desiredState string) string {
	// Primary -> Secondary transition
	if (currentStatus == "primary" || currentStatus == "resync") &&
		(currentSpec == "secondary" || desiredState == "secondary") {
		return "Transitioning from primary to secondary - temporary degraded state is expected"
	}

	// Secondary -> Primary transition
	if (currentStatus == "secondary" || currentStatus == "resync") &&
		(currentSpec == "primary" || desiredState == "primary") {
		return "Promoting from secondary to primary - please wait for resync to complete"
	}

	// Error state but expected during transition
	if currentStatus == "error" &&
		((currentSpec == "secondary" && desiredState == "secondary") ||
			(currentSpec == "primary" && desiredState == "primary")) {
		return "Temporary error state during replication state transition - this should resolve shortly"
	}

	// Spec doesn't match status - transition in progress
	if currentSpec != currentStatus && currentStatus != "error" {
		if currentSpec == "primary" {
			return "Transitioning to primary state - please wait for completion"
		} else if currentSpec == "secondary" {
			return "Transitioning to secondary state - please wait for completion"
		}
	}

	// General resync message
	if currentStatus == "resync" {
		return "Volume is resyncing - please wait for completion"
	}

	return ""
}

// GetErrorMessage returns an appropriate message for a specific error condition
func (m *Manager) GetErrorMessage(ctx context.Context, name, namespace string) string {
	volumeReplication := &replicationv1alpha1.VolumeReplication{}

	err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, volumeReplication)
	if err != nil {
		return ""
	}

	// Check for special error conditions
	for _, condition := range volumeReplication.Status.Conditions {
		if condition.Type == "Degraded" && condition.Status == metav1.ConditionTrue {
			// Handle "last sync time not found" error specifically
			if condition.Reason == "Error" && strings.Contains(condition.Message, "last sync time not found") {
				return "Missing snapshot data - this is normal for newly created volumes"
			}

			// Handle "failed to resync" error
			if strings.Contains(condition.Message, "failed to resync") {
				return "Volume is experiencing temporary resync issues - this should resolve automatically"
			}

			// Can add more specific error message handling here as needed
		}
	}

	return ""
}

// ProcessVolumeReplications handles the processing of all VolumeReplications for a FailoverPolicy
func (m *Manager) ProcessVolumeReplications(ctx context.Context, namespace string, vrNames []string, desiredState, mode string) (int, bool, string) {
	log := log.FromContext(ctx)
	var pendingUpdates int
	var failoverError bool
	var failoverErrorMessage string

	// For safe mode, first check if all VolumeReplications are ready for primary transition
	if mode == "safe" && strings.ToLower(desiredState) == "primary" {
		allReady := true
		for _, vrName := range vrNames {
			currentState, err := m.GetCurrentVolumeReplicationState(ctx, vrName, namespace)
			if err != nil {
				failoverErrorMessage = fmt.Sprintf("Failed to retrieve state for VolumeReplication %s", vrName)
				return 0, true, failoverErrorMessage
			}

			// In safe mode, volumes must be either already primary or secondary before transitioning
			if currentState != "primary" && currentState != "secondary" {
				log.Info("Safe mode: waiting for VolumeReplication to be in valid state",
					"VolumeReplication", vrName,
					"CurrentState", currentState,
					"ValidStates", []string{"primary", "secondary"})
				allReady = false
				pendingUpdates++
			}
		}

		// Only return early if not all volumes are ready
		if !allReady {
			return pendingUpdates, false, ""
		}
	}

	// Process all VolumeReplications
	for _, vrName := range vrNames {
		log.Info("Checking VolumeReplication", "VolumeReplication", vrName)

		// Detect VolumeReplication errors
		errorMessage, errorDetected := m.CheckVolumeReplicationError(ctx, vrName, namespace)
		if errorDetected {
			failoverErrorMessage = fmt.Sprintf("VolumeReplication %s: %s", vrName, errorMessage)
			failoverError = true
			continue
		}

		// Get current replication state
		currentState, err := m.GetCurrentVolumeReplicationState(ctx, vrName, namespace)
		if err != nil {
			failoverErrorMessage = fmt.Sprintf("Failed to retrieve state for VolumeReplication %s", vrName)
			failoverError = true
			continue
		}

		// If the current state already matches the desired state, no update is needed
		if strings.EqualFold(currentState, desiredState) {
			log.Info("VolumeReplication is already in the desired state",
				"VolumeReplication", vrName,
				"State", currentState)
			continue
		}

		// Attempt to update VolumeReplication
		pending, err := m.UpdateVolumeReplication(ctx, vrName, namespace, desiredState)
		if err != nil {
			log.Error(err, "Failed to update VolumeReplication", "VolumeReplication", vrName)
			return 0, true, "Failed to update VolumeReplication"
		}
		if pending {
			pendingUpdates++
		}
	}

	return pendingUpdates, failoverError, failoverErrorMessage
}
