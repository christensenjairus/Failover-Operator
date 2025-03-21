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
	"encoding/json"
	"fmt"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/log"
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
	*BaseManager
}

// NewVolumeStateManager creates a new volume state manager
func NewVolumeStateManager(baseManager *BaseManager) *VolumeStateManager {
	return &VolumeStateManager{
		BaseManager: baseManager,
	}
}

// GetVolumeState retrieves the current volume state for a failover group
func (v *VolumeStateManager) GetVolumeState(ctx context.Context, namespace, groupName string) (string, error) {
	// Get the group state which contains volume state info
	groupState, err := v.GetGroupState(ctx, namespace, groupName)
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
	config, err := v.GetGroupConfig(ctx, namespace, groupName)
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
	return v.UpdateGroupConfig(ctx, config)
}

// UpdateHeartbeat updates the heartbeat timestamp for a cluster
func (v *VolumeStateManager) UpdateHeartbeat(ctx context.Context, namespace, groupName, clusterName string) error {
	logger := log.FromContext(ctx)

	// Robust nil checks
	if v == nil {
		logger.Error(nil, "VolumeStateManager is nil")
		return fmt.Errorf("invalid VolumeStateManager: manager is nil")
	}

	if v.BaseManager == nil {
		logger.Error(nil, "BaseManager is nil")
		return fmt.Errorf("invalid VolumeStateManager: BaseManager is nil")
	}

	if v.client == nil {
		logger.Error(nil, "DynamoDB client is nil")
		return fmt.Errorf("invalid VolumeStateManager: DynamoDB client is nil")
	}

	// Check parameters
	if namespace == "" {
		logger.Error(nil, "Namespace is empty")
		return fmt.Errorf("namespace cannot be empty")
	}

	if groupName == "" {
		logger.Error(nil, "Group name is empty")
		return fmt.Errorf("group name cannot be empty")
	}

	// Handle clusterName
	if clusterName == "" {
		if v.clusterName == "" {
			logger.Error(nil, "No cluster name available")
			clusterName = "unknown-cluster" // Use a fallback name
		} else {
			clusterName = v.clusterName // Use manager's cluster name as fallback
		}
	}

	// Log what we're doing
	logger.V(1).Info("Updating heartbeat",
		"namespace", namespace,
		"groupName", groupName,
		"clusterName", clusterName)

	// Create a default record structure
	defaultStatus := &ClusterStatusRecord{
		GroupNamespace: namespace,
		GroupName:      groupName,
		ClusterName:    clusterName,
		Health:         HealthOK,
		State:          StatePrimary, // Default to primary, will be updated by reconciliation
		Components:     "{}",
		LastHeartbeat:  time.Now(),
	}

	// Try to get the existing status, but continue with default if error
	status, err := v.GetClusterStatus(ctx, namespace, groupName, clusterName)
	if err != nil || status == nil {
		if err != nil {
			logger.V(1).Info("Failed to get cluster status, will create a default one",
				"error", err)
		} else {
			logger.V(1).Info("No existing cluster status found, will create a default one")
		}

		// Use the default record
		status = defaultStatus
	} else {
		// Update the heartbeat time of the existing record
		status.LastHeartbeat = time.Now()
	}

	// Convert components string to map
	var componentsMap map[string]ComponentStatus
	if status.Components != "" && status.Components != "{}" {
		if err := json.Unmarshal([]byte(status.Components), &componentsMap); err != nil {
			logger.Error(err, "Failed to unmarshal components, using empty components")
			componentsMap = make(map[string]ComponentStatus)
		}
	} else {
		componentsMap = make(map[string]ComponentStatus)
	}

	// Marshal the components map to get a valid JSON string
	componentsJSON, err := json.Marshal(componentsMap)
	if err != nil {
		logger.Error(err, "Failed to marshal components, using empty components")
		componentsJSON = []byte("{}")
	}

	// Use UpdateClusterStatus directly instead of UpdateClusterStatusLegacy
	return v.UpdateClusterStatus(
		ctx,
		namespace,
		groupName,
		clusterName,
		status.Health,
		status.State,
		string(componentsJSON),
	)
}

// RemoveVolumeState removes the volume state information
func (v *VolumeStateManager) RemoveVolumeState(ctx context.Context, namespace, groupName string) error {
	// Get current group config
	config, err := v.GetGroupConfig(ctx, namespace, groupName)
	if err != nil {
		return fmt.Errorf("failed to get group config: %w", err)
	}

	// Remove volume state fields if metadata exists
	if config.Metadata != nil {
		delete(config.Metadata, "volumeState")
		delete(config.Metadata, "volumeStateUpdateTime")
	}

	// Save the updated config
	return v.UpdateGroupConfig(ctx, config)
}
