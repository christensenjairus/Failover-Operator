package volumereplication

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/errors"
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
func (m *Manager) UpdateVolumeReplication(ctx context.Context, name, namespace, state string) error {
	log := log.FromContext(ctx)
	volumeReplication := &replicationv1alpha1.VolumeReplication{}

	// Fetch the VolumeReplication object
	err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, volumeReplication)
	if err != nil {
		log.Error(err, "Failed to get VolumeReplication", "VolumeReplication", name)
		return client.IgnoreNotFound(err)
	}

	currentSpec := string(volumeReplication.Spec.ReplicationState)
	currentStatus := strings.ToLower(string(volumeReplication.Status.State))
	desiredState := strings.ToLower(state)

	// Check if we have no status yet
	if currentStatus == "" {
		if strings.EqualFold(currentSpec, desiredState) {
			log.Info("VolumeReplication has no status yet but spec is already correct",
				"name", name,
				"specState", currentSpec,
				"desiredState", desiredState)
			// Spec is already correct, just wait for controller to update status
			return nil
		}
	}

	log.Info("Checking VolumeReplication state",
		"name", name,
		"currentSpec", currentSpec,
		"currentStatus", currentStatus,
		"desiredState", desiredState)

	// Handle primary->secondary transition specially
	if currentStatus == "primary" && desiredState == "secondary" {
		if currentSpec != state {
			log.Info("Initiating primary->secondary transition - degraded state expected temporarily",
				"name", name)
			volumeReplication.Spec.ReplicationState = replicationv1alpha1.ReplicationState(state)
			if err := m.client.Update(ctx, volumeReplication); err != nil {
				log.Error(err, "Failed to update VolumeReplication", "VolumeReplication", name)
				return err
			}
		}
		return nil
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
							return err
						}
					}
					return nil
				}
			}
		}
	}

	// If transitioning to primary, ensure current status is either secondary or already primary
	if desiredState == "primary" && currentStatus != "secondary" && currentStatus != "primary" {
		log.Info("Cannot transition to primary - current status must be secondary or primary",
			"name", name,
			"currentStatus", currentStatus)
		return nil
	}

	// Only update spec if it doesn't match desired state and status is stable
	if currentSpec != state {
		// Check if status is in a stable state before updating
		if currentStatus == "error" {
			log.Info("Cannot update spec - VolumeReplication is in error state",
				"name", name)
			return nil
		}

		log.Info("Updating VolumeReplication spec",
			"name", name,
			"currentSpec", currentSpec,
			"desiredState", state)

		volumeReplication.Spec.ReplicationState = replicationv1alpha1.ReplicationState(state)
		if err := m.client.Update(ctx, volumeReplication); err != nil {
			log.Error(err, "Failed to update VolumeReplication", "VolumeReplication", name)
			return err
		}
		return nil
	}

	// Check if status matches spec
	if currentStatus != desiredState {
		log.Info("VolumeReplication status not yet matching spec",
			"name", name,
			"statusState", currentStatus,
			"desiredState", desiredState)
		return nil
	}

	return nil
}

// CheckVolumeReplicationError checks if a VolumeReplication resource has errors
func (m *Manager) CheckVolumeReplicationError(ctx context.Context, name, namespace string) (string, bool) {
	log := log.FromContext(ctx)
	volumeReplication := &replicationv1alpha1.VolumeReplication{}

	// Fetch the VolumeReplication object
	err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, volumeReplication)
	if err != nil {
		// If the resource doesn't exist, don't treat this as an error condition
		if client.IgnoreNotFound(err) == nil {
			log.Info("VolumeReplication not found during error check - it may need to be created manually",
				"VolumeReplication", name,
				"Namespace", namespace)
			return "", false
		}

		// For other types of errors, report them
		log.Error(err, "Failed to get VolumeReplication", "VolumeReplication", name)
		return fmt.Sprintf("Error accessing VolumeReplication: %v", err), true
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

			// Primary -> Secondary transition: always expect temporary degraded state
			if currentSpec == "secondary" && currentStatus == "primary" {
				isTransitioning = true
			}

			// After a spec update but before status reflects it, we're in transition
			if currentSpec != currentStatus && currentStatus != "error" {
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
		return "notfound", client.IgnoreNotFound(err)
	}

	// Get the status state
	currentStatus := strings.ToLower(string(volumeReplication.Status.State))

	// If the status state is empty, it could be because the CRD was just created
	// In this case, check if we have a spec state
	if currentStatus == "" {
		currentSpec := string(volumeReplication.Spec.ReplicationState)
		if currentSpec != "" {
			log := log.FromContext(ctx)
			log.Info("VolumeReplication has no status state yet, using spec state as current state",
				"name", name,
				"specState", currentSpec)
			// Return the spec state with a marker that it's from spec
			return strings.ToLower(currentSpec) + "-pending", nil
		}
		// No spec state either, this is really unknown
		return "unknown", nil
	}

	// Convert to lowercase to ensure consistent comparison
	return currentStatus, nil
}

// isTransitionalState checks if the VolumeReplication is in a transitional state
func (m *Manager) isTransitionalState(currentSpec, currentStatus, desiredState string) bool {
	// Primary -> Secondary transition
	if currentStatus == "primary" && currentSpec == "secondary" && desiredState == "secondary" {
		return true
	}

	// Secondary -> Primary transition
	if currentStatus == "secondary" && currentSpec == "primary" && desiredState == "primary" {
		return true
	}

	return false
}

// GetTransitionMessage returns an appropriate message for a volume in transition
func (m *Manager) GetTransitionMessage(currentSpec, currentStatus, desiredState string) string {
	// If currentStatus is empty, likely the VR resource exists but hasn't been properly initialized
	if currentStatus == "" {
		return "VolumeReplication exists but status not yet reported by controller"
	}

	// Primary -> Secondary transition
	if currentStatus == "primary" && (currentSpec == "secondary" || desiredState == "secondary") {
		return "Transitioning from primary to secondary - temporary degraded state is expected"
	}

	// Secondary -> Primary transition
	if currentStatus == "secondary" && (currentSpec == "primary" || desiredState == "primary") {
		return "Promoting from secondary to primary"
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

	// Current status matches desired state
	if strings.EqualFold(currentStatus, desiredState) {
		if currentStatus == "primary" {
			return "Volume is in primary read-write mode"
		} else if currentStatus == "secondary" {
			return "Volume is in secondary read-only mode"
		}
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

			// Can add more specific error message handling here as needed
		}
	}

	return ""
}

// AreAllVolumesInDesiredState checks if all volume replications are in the desired state
// Returns a boolean indicating if all volumes are in the desired state and a message
func (m *Manager) AreAllVolumesInDesiredState(ctx context.Context, namespace string, volReps []string, desiredState string) (bool, string) {
	log := log.FromContext(ctx).WithName("volumereplication-manager")

	// Map active/passive to primary/secondary for volume replication
	volRepDesiredState := desiredState
	if desiredState == "active" {
		volRepDesiredState = "primary"
	} else if desiredState == "passive" {
		volRepDesiredState = "secondary"
	}

	if len(volReps) == 0 {
		return true, "No VolumeReplications to check"
	}

	log.Info("Checking if all volumes are in desired state",
		"count", len(volReps),
		"namespace", namespace,
		"desiredState", desiredState,
		"volRepDesiredState", volRepDesiredState)

	allInDesiredState := true
	detailedStatus := []string{}

	for _, name := range volReps {
		volumeReplication := &replicationv1alpha1.VolumeReplication{}
		err := m.client.Get(ctx, types.NamespacedName{
			Name:      name,
			Namespace: namespace,
		}, volumeReplication)

		if err != nil {
			if errors.IsNotFound(err) {
				log.Info("VolumeReplication not found", "name", name, "namespace", namespace)
				allInDesiredState = false
				detailedStatus = append(detailedStatus, fmt.Sprintf("%s: notfound", name))
				continue
			}
			log.Error(err, "Failed to get VolumeReplication", "name", name, "namespace", namespace)
			allInDesiredState = false
			detailedStatus = append(detailedStatus, fmt.Sprintf("%s: error", name))
			continue
		}

		// Special case if we're in a resync state
		if IsResyncState(volumeReplication) {
			log.Info("VolumeReplication is in Resync state, still in progress", "name", name)
			allInDesiredState = false
			detailedStatus = append(detailedStatus, fmt.Sprintf("%s: resyncing", name))
			continue
		}

		// Check if the volume replication is in the desired spec state
		currentStatus := m.getCurrentStatus(volumeReplication)
		currentSpec := m.getCurrentSpec(volumeReplication)

		log.V(1).Info("VolumeReplication current state",
			"name", name,
			"specState", currentSpec,
			"statusState", currentStatus,
			"desiredState", volRepDesiredState)

		// First case: Current status already matches desired state
		if strings.EqualFold(currentStatus, volRepDesiredState) {
			detailedStatus = append(detailedStatus, fmt.Sprintf("%s: %s", name, currentStatus))
			continue
		}

		// Second case: Status doesn't match, but spec does - we're in transition
		if strings.EqualFold(currentSpec, volRepDesiredState) {
			log.Info("VolumeReplication is still transitioning to desired state",
				"name", name,
				"currentStatus", currentStatus,
				"desiredState", volRepDesiredState)
			allInDesiredState = false
			detailedStatus = append(detailedStatus, fmt.Sprintf("%s: %s-pending", name, currentSpec))
			continue
		}

		// If we get here, neither the spec nor status match the desired state
		log.Info("VolumeReplication is not in desired state",
			"name", name,
			"currentStatus", currentStatus,
			"currentSpec", currentSpec,
			"desiredState", volRepDesiredState)
		allInDesiredState = false
		detailedStatus = append(detailedStatus, fmt.Sprintf("%s: %s", name, currentStatus))
	}

	return allInDesiredState, strings.Join(detailedStatus, ", ")
}

// ProcessVolumeReplications processes all volumereplication resources based on desired state
func (m *Manager) ProcessVolumeReplications(ctx context.Context, namespace string, volReps []string, desiredState string) error {
	log := log.FromContext(ctx).WithName("volumereplication-manager")

	// Normalize the desired state to ensure consistent handling
	volRepDesiredState := "primary" // Default

	// Handle all variations of state names for consistent behavior
	normalizedState := strings.ToUpper(desiredState)
	if strings.EqualFold(desiredState, "active") ||
		strings.EqualFold(desiredState, "primary") ||
		normalizedState == "PRIMARY" {
		volRepDesiredState = "primary"
	} else if strings.EqualFold(desiredState, "passive") ||
		strings.EqualFold(desiredState, "secondary") ||
		strings.EqualFold(desiredState, "standby") ||
		normalizedState == "STANDBY" {
		volRepDesiredState = "secondary"
	}

	log.Info("Processing VolumeReplications",
		"count", len(volReps),
		"namespace", namespace,
		"desiredState", desiredState,
		"normalizedState", normalizedState,
		"volRepDesiredState", volRepDesiredState)

	if len(volReps) == 0 {
		log.Info("No VolumeReplications to process")
		return nil
	}

	// Process each volume replication
	for _, name := range volReps {
		log.Info("Processing VolumeReplication", "name", name, "namespace", namespace)

		// Get the VolumeReplication resource
		volumeReplication := &replicationv1alpha1.VolumeReplication{}
		err := m.client.Get(ctx, types.NamespacedName{
			Name:      name,
			Namespace: namespace,
		}, volumeReplication)

		if err != nil {
			if errors.IsNotFound(err) {
				log.Info("VolumeReplication not found", "name", name, "namespace", namespace)
				continue
			}
			return err
		}

		// Get current spec and status
		currentSpec := strings.ToLower(string(volumeReplication.Spec.ReplicationState))
		currentStatus := strings.ToLower(string(volumeReplication.Status.State))

		log.Info("VolumeReplication current state",
			"name", name,
			"currentSpec", currentSpec,
			"currentStatus", currentStatus,
			"desiredState", volRepDesiredState)

		// Skip if volumereplication is already in correct state
		if currentSpec == volRepDesiredState {
			// Already in correct state
			log.Info("VolumeReplication already in correct state",
				"name", name,
				"state", volRepDesiredState)
			continue
		}

		log.Info("Updating VolumeReplication state",
			"name", name,
			"currentState", currentSpec,
			"desiredState", volRepDesiredState)

		// Set the desired state and update the resource
		volumeReplication.Spec.ReplicationState = replicationv1alpha1.ReplicationState(volRepDesiredState)
		if err := m.client.Update(ctx, volumeReplication); err != nil {
			log.Error(err, "Failed to update VolumeReplication", "name", name)
			return err
		}

		log.Info("Successfully updated VolumeReplication state",
			"name", name,
			"namespace", namespace,
			"newState", volRepDesiredState)
	}

	return nil
}

// getCurrentStatus retrieves the current status of a VolumeReplication resource
func (m *Manager) getCurrentStatus(volumeReplication *replicationv1alpha1.VolumeReplication) string {
	return strings.ToLower(string(volumeReplication.Status.State))
}

// getCurrentSpec retrieves the current spec of a VolumeReplication resource
func (m *Manager) getCurrentSpec(volumeReplication *replicationv1alpha1.VolumeReplication) string {
	return string(volumeReplication.Spec.ReplicationState)
}

// updateVolumeReplicationState updates the state of a VolumeReplication resource
func (m *Manager) updateVolumeReplicationState(ctx context.Context, volumeReplication *replicationv1alpha1.VolumeReplication, desiredState string) error {
	return m.UpdateVolumeReplication(ctx, volumeReplication.Name, volumeReplication.Namespace, desiredState)
}

// IsResyncState checks if a VolumeReplication is in a resync state
func IsResyncState(volumeReplication *replicationv1alpha1.VolumeReplication) bool {
	return strings.ToLower(string(volumeReplication.Spec.ReplicationState)) == "resyncing"
}
