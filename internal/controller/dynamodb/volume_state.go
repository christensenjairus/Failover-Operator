/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package dynamodb

import (
	"context"
	"fmt"
	"time"
)

// VolumeState constants for the failover process
const (
	VolumeStateReady     = "READY_FOR_PROMOTION"
	VolumeStatePromoted  = "PROMOTED"
	VolumeStateCompleted = "COMPLETED"
	VolumeStateFailed    = "FAILED"
)

// VolumeStateManager provides functionality for tracking volume state during failovers
type VolumeStateManager struct {
	stateManager *StateManager
}

// NewVolumeStateManager creates a new volume state manager
func NewVolumeStateManager(stateManager *StateManager) *VolumeStateManager {
	return &VolumeStateManager{
		stateManager: stateManager,
	}
}

// GetVolumeState retrieves the current volume state for a failover group
func (v *VolumeStateManager) GetVolumeState(ctx context.Context, namespace, groupName string) (string, error) {
	// Get the group state which contains volume state info
	groupState, err := v.stateManager.GetGroupState(ctx, namespace, groupName)
	if err != nil {
		return "", fmt.Errorf("failed to get group state: %w", err)
	}

	// We use the VolumeState field from GroupState
	// This is a known field in GroupState that we're accessing
	return groupState.VolumeState, nil
}

// SetVolumeState updates the volume state for a failover group
func (v *VolumeStateManager) SetVolumeState(ctx context.Context, namespace, groupName, state string) error {
	// In a real implementation, we would update the group state in DynamoDB
	// For this implementation, we'll use GroupConfig since it has a Metadata field

	// Get current group config
	config, err := v.stateManager.GetGroupConfig(ctx, namespace, groupName)
	if err != nil {
		return fmt.Errorf("failed to get group config: %w", err)
	}

	// Store volume state information
	// Note: This assumes we've added the Metadata field to GroupConfigRecord
	// If Metadata doesn't exist, this will need to be updated
	if config.Metadata == nil {
		// If Metadata is missing, we just log the state change but can't store it
		return fmt.Errorf("metadata field not available for storing volume state")
	}

	// Update the volume state fields
	config.Metadata["volumeState"] = state
	config.Metadata["volumeStateUpdateTime"] = time.Now().Format(time.RFC3339)

	// Save the updated config
	return v.stateManager.UpdateGroupConfig(ctx, config)
}

// UpdateHeartbeat updates the heartbeat timestamp for a cluster
func (v *VolumeStateManager) UpdateHeartbeat(ctx context.Context, namespace, groupName, clusterName string) error {
	// Get all cluster statuses
	statuses, err := v.stateManager.GetAllClusterStatuses(ctx, namespace, groupName)
	if err != nil {
		return fmt.Errorf("failed to get cluster statuses: %w", err)
	}

	// Get this cluster's status
	status, exists := statuses[clusterName]
	if !exists {
		return fmt.Errorf("cluster status not found for %s", clusterName)
	}

	// Update the heartbeat time
	status.LastHeartbeat = time.Now()

	// Update the status
	return v.stateManager.UpdateClusterStatusLegacy(
		ctx,
		namespace,
		groupName,
		status.Health,
		status.State,
		nil, // Components field is updated elsewhere
	)
}

// RemoveVolumeState removes the volume state information
func (v *VolumeStateManager) RemoveVolumeState(ctx context.Context, namespace, groupName string) error {
	// Get current group config
	config, err := v.stateManager.GetGroupConfig(ctx, namespace, groupName)
	if err != nil {
		return fmt.Errorf("failed to get group config: %w", err)
	}

	// Remove volume state fields if metadata exists
	if config.Metadata != nil {
		delete(config.Metadata, "volumeState")
		delete(config.Metadata, "volumeStateUpdateTime")
	}

	// Save the updated config
	return v.stateManager.UpdateGroupConfig(ctx, config)
}
