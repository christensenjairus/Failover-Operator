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

package failovergroup

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	crdv1alpha1 "github.com/christensenjairus/Failover-Operator/api/v1alpha1"
	"github.com/christensenjairus/Failover-Operator/internal/controller/dynamodb"
)

// Manager handles FailoverGroup operations and synchronization with DynamoDB
type Manager struct {
	client.Client
	Log         logr.Logger
	ClusterName string // Current cluster name

	// DynamoDB Service for state coordination
	DynamoDBManager *dynamodb.DynamoDBService
}

// NewManager creates a new FailoverGroup manager
func NewManager(client client.Client, clusterName string, log logr.Logger) *Manager {
	return &Manager{
		Client:      client,
		ClusterName: clusterName,
		Log:         log.WithName("failovergroup-manager"),
	}
}

// SetDynamoDBManager sets the DynamoDB manager
func (m *Manager) SetDynamoDBManager(dbManager *dynamodb.DynamoDBService) {
	m.DynamoDBManager = dbManager
}

// SyncWithDynamoDB synchronizes the FailoverGroup state with DynamoDB
// This should be called regularly to ensure the local view matches the global state
func (m *Manager) SyncWithDynamoDB(ctx context.Context, failoverGroup *crdv1alpha1.FailoverGroup) error {
	log := m.Log.WithValues(
		"namespace", failoverGroup.Namespace,
		"name", failoverGroup.Name,
	)

	if m.DynamoDBManager == nil {
		log.Info("DynamoDB manager not configured, skipping synchronization")
		return nil
	}

	// Get the current state from DynamoDB
	groupState, err := m.DynamoDBManager.GetGroupState(ctx, failoverGroup.Namespace, failoverGroup.Name)
	if err != nil {
		log.Error(err, "Failed to get group state from DynamoDB")
		// Continue with a nil groupState - don't fail the sync completely
		// This allows the operator to work even when DynamoDB is unavailable
		// or when no state has been written yet
	}

	// Update the local FailoverGroup status based on the global state
	return m.updateLocalStatus(ctx, failoverGroup, groupState)
}

// UpdateDynamoDBStatus updates the cluster's status in DynamoDB
func (m *Manager) UpdateDynamoDBStatus(ctx context.Context, failoverGroup *crdv1alpha1.FailoverGroup) error {
	log := m.Log.WithValues(
		"namespace", failoverGroup.Namespace,
		"name", failoverGroup.Name,
	)

	if m.DynamoDBManager == nil {
		log.Info("DynamoDB manager not configured, skipping status update")
		return nil
	}

	// Basic status data with just this cluster's health
	statusData := &dynamodb.StatusData{
		// Fill in with empty values initially
		Workloads:          []dynamodb.ResourceStatus{},
		NetworkResources:   []dynamodb.ResourceStatus{},
		FluxResources:      []dynamodb.ResourceStatus{},
		VolumeReplications: []dynamodb.WorkloadReplicationStatus{},
	}

	// Determine role based on the FailoverGroup status
	role := m.determineClusterRole(failoverGroup)

	// We could populate more detailed status data here in a real implementation

	// Update the status in DynamoDB
	return m.DynamoDBManager.UpdateClusterStatus(
		ctx,
		failoverGroup.Namespace,
		failoverGroup.Name,
		"OK", // Default health status
		role,
		statusData,
	)
}

// StartPeriodicSynchronization starts a goroutine to periodically sync with DynamoDB
func (m *Manager) StartPeriodicSynchronization(ctx context.Context, intervalSeconds int) {
	if intervalSeconds <= 0 {
		intervalSeconds = 30 // Default to 30 seconds
	}

	m.Log.Info("Starting periodic DynamoDB synchronization", "intervalSeconds", intervalSeconds)

	go func() {
		ticker := time.NewTicker(time.Duration(intervalSeconds) * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				m.Log.Info("Stopping periodic DynamoDB synchronization")
				return
			case <-ticker.C:
				if err := m.syncAllFailoverGroups(ctx); err != nil {
					m.Log.Error(err, "Error during periodic synchronization")
				}
			}
		}
	}()
}

// syncAllFailoverGroups synchronizes all FailoverGroup resources with DynamoDB
func (m *Manager) syncAllFailoverGroups(ctx context.Context) error {
	var failoverGroupList crdv1alpha1.FailoverGroupList
	if err := m.Client.List(ctx, &failoverGroupList); err != nil {
		return fmt.Errorf("failed to list FailoverGroups: %w", err)
	}

	for i := range failoverGroupList.Items {
		failoverGroup := &failoverGroupList.Items[i]
		if err := m.SyncWithDynamoDB(ctx, failoverGroup); err != nil {
			m.Log.Error(err, "Failed to sync FailoverGroup with DynamoDB",
				"namespace", failoverGroup.Namespace,
				"name", failoverGroup.Name)
			continue
		}

		if err := m.UpdateDynamoDBStatus(ctx, failoverGroup); err != nil {
			m.Log.Error(err, "Failed to update DynamoDB status",
				"namespace", failoverGroup.Namespace,
				"name", failoverGroup.Name)
		}
	}

	return nil
}

// determineClusterRole determines the role of the current cluster based on the FailoverGroup status
func (m *Manager) determineClusterRole(failoverGroup *crdv1alpha1.FailoverGroup) string {
	// Check if this cluster is the active cluster in the global state
	if failoverGroup.Status.GlobalState.ActiveCluster == m.ClusterName {
		return "PRIMARY"
	}
	return "STANDBY"
}

// updateLocalStatus updates the local FailoverGroup status based on the global state from DynamoDB
func (m *Manager) updateLocalStatus(ctx context.Context, failoverGroup *crdv1alpha1.FailoverGroup, groupState *dynamodb.ManagerGroupState) error {
	log := m.Log.WithValues(
		"namespace", failoverGroup.Namespace,
		"name", failoverGroup.Name,
	)
	// Update the FailoverGroup status based on the DynamoDB state
	updated := false

	// Get config directly from DynamoDB service
	var config *dynamodb.GroupConfigRecord
	var err error
	if m.DynamoDBManager != nil {
		config, err = m.DynamoDBManager.GetGroupConfig(ctx, failoverGroup.Namespace, failoverGroup.Name)
		if err != nil {
			log.Error(err, "Failed to get group config, will use current state")
			// Continue with what we have - don't fail the sync completely
		}
	} else {
		log.Info("DynamoDB manager not configured, using default config")
	}

	// Initialize GlobalState if needed
	if failoverGroup.Status.GlobalState.ActiveCluster == "" && failoverGroup.Status.GlobalState.ThisCluster == "" {
		// This means it's likely not initialized yet
		failoverGroup.Status.GlobalState = crdv1alpha1.GlobalStateInfo{
			ThisCluster: m.ClusterName,
		}
		updated = true
	}

	// Always ensure ThisCluster is set to the current cluster
	if failoverGroup.Status.GlobalState.ThisCluster != m.ClusterName {
		failoverGroup.Status.GlobalState.ThisCluster = m.ClusterName
		updated = true
	}

	// Determine active cluster (owner cluster)
	activeCluster := failoverGroup.Status.GlobalState.ActiveCluster
	if config != nil && config.OwnerCluster != "" {
		activeCluster = config.OwnerCluster
	} else if activeCluster == "" {
		// If no active cluster is set and we couldn't get it from DynamoDB,
		// use this cluster as the active cluster
		activeCluster = m.ClusterName
		log.Info("No active cluster found in config, using current cluster",
			"cluster", m.ClusterName)
	}

	// Update owner cluster if different
	if failoverGroup.Status.GlobalState.ActiveCluster != activeCluster {
		log.Info("Updating active cluster",
			"from", failoverGroup.Status.GlobalState.ActiveCluster,
			"to", activeCluster)
		failoverGroup.Status.GlobalState.ActiveCluster = activeCluster
		updated = true
	}

	// Get all cluster statuses to build a complete picture
	var clusterStatuses map[string]*dynamodb.ClusterStatusRecord
	if m.DynamoDBManager != nil {
		clusterStatuses, err = m.DynamoDBManager.GetAllClusterStatuses(ctx, failoverGroup.Namespace, failoverGroup.Name)
		if err != nil {
			log.Error(err, "Failed to get all cluster statuses")
			// Initialize an empty map to prevent nil pointer dereference
			clusterStatuses = make(map[string]*dynamodb.ClusterStatusRecord)
		} else {
			// Add debug info
			clusterNames := make([]string, 0, len(clusterStatuses))
			for name := range clusterStatuses {
				clusterNames = append(clusterNames, name)
			}
			log.V(1).Info("Retrieved cluster statuses from DynamoDB",
				"count", len(clusterStatuses),
				"clusters", clusterNames)
		}
	} else {
		log.Info("DynamoDB manager not configured, using empty cluster status map")
		clusterStatuses = make(map[string]*dynamodb.ClusterStatusRecord)
	}

	// Build a map of existing clusters in the status for easy lookup
	existingClusters := make(map[string]int)
	for i, cluster := range failoverGroup.Status.GlobalState.Clusters {
		existingClusters[cluster.Name] = i
	}

	// Update existing clusters and add new ones from DynamoDB
	var newClusters []crdv1alpha1.ClusterInfo

	// Always include this cluster (even if not found in DynamoDB yet)
	thisClusterFound := false

	// Process clusters from DynamoDB
	for clusterName, status := range clusterStatuses {
		// Skip nil status entries
		if status == nil {
			log.Info("Skipping nil status for cluster", "clusterName", clusterName)
			continue
		}

		// Format heartbeat time, handling nil or zero values
		heartbeatTime := time.Now().Format(time.RFC3339)
		if !status.LastHeartbeat.IsZero() {
			heartbeatTime = status.LastHeartbeat.Format(time.RFC3339)
		}

		clusterInfo := crdv1alpha1.ClusterInfo{
			Name:          clusterName,
			Role:          status.State,
			Health:        status.Health,
			LastHeartbeat: heartbeatTime,
		}

		if clusterName == m.ClusterName {
			thisClusterFound = true
		}

		newClusters = append(newClusters, clusterInfo)
	}

	// Add this cluster if not found in DynamoDB
	if !thisClusterFound {
		role := "STANDBY"
		if activeCluster == m.ClusterName {
			role = "PRIMARY"
		}

		newClusters = append(newClusters, crdv1alpha1.ClusterInfo{
			Name:          m.ClusterName,
			Role:          role,
			Health:        "UNKNOWN", // Use "UNKNOWN" until proper health is determined
			LastHeartbeat: time.Now().Format(time.RFC3339),
		})
	}

	// If clusters changed, update the status
	if len(newClusters) > 0 {
		failoverGroup.Status.GlobalState.Clusters = newClusters
		updated = true
	}

	// Configure resources based on role if active cluster changed
	if updated && failoverGroup.Status.GlobalState.ActiveCluster != "" {
		if failoverGroup.Status.GlobalState.ActiveCluster == m.ClusterName {
			if err := m.configureAsPrimary(ctx, failoverGroup); err != nil {
				m.Log.Error(err, "Failed to configure as PRIMARY")
				return err
			}
		} else {
			if err := m.configureAsStandby(ctx, failoverGroup); err != nil {
				m.Log.Error(err, "Failed to configure as STANDBY")
				return err
			}
		}
	}

	// Update the FailoverGroup resource if status changed
	if updated {
		// Implement retry logic for handling resource conflicts
		maxRetries := 3
		for retry := 0; retry < maxRetries; retry++ {
			err := m.Client.Status().Update(ctx, failoverGroup)
			if err == nil {
				log.Info("Updated FailoverGroup status from DynamoDB",
					"activeCluster", failoverGroup.Status.GlobalState.ActiveCluster,
					"clusterCount", len(failoverGroup.Status.GlobalState.Clusters),
					"retry", retry)
				return nil
			}

			// Check if it's a conflict error
			if k8sErrors.IsConflict(err) {
				// If this is a conflict, get the latest version of the resource
				log.Info("Detected conflict while updating FailoverGroup status, retrying with latest version",
					"retry", retry+1, "maxRetries", maxRetries)

				// Get the latest version of the FailoverGroup
				latestFG := &crdv1alpha1.FailoverGroup{}
				namespacedName := types.NamespacedName{
					Namespace: failoverGroup.Namespace,
					Name:      failoverGroup.Name,
				}

				if getErr := m.Client.Get(ctx, namespacedName, latestFG); getErr != nil {
					log.Error(getErr, "Failed to get latest FailoverGroup resource")
					return getErr
				}

				// Transfer our status changes to the latest version
				// Preserve our changes to GlobalState, which is what we're primarily updating
				latestFG.Status.GlobalState.ThisCluster = failoverGroup.Status.GlobalState.ThisCluster
				latestFG.Status.GlobalState.ActiveCluster = failoverGroup.Status.GlobalState.ActiveCluster
				latestFG.Status.GlobalState.Clusters = failoverGroup.Status.GlobalState.Clusters

				// Update our reference to point to the latest version
				failoverGroup = latestFG

				// Very short backoff before retry
				time.Sleep(time.Millisecond * 100 * time.Duration(retry+1))
				continue
			}

			// For other errors, return immediately
			log.Error(err, "Failed to update FailoverGroup status")
			return err
		}

		// If we exhausted retries
		return fmt.Errorf("failed to update FailoverGroup status after %d retries", maxRetries)
	}

	return nil
}

// configureAsPrimary configures resources when this cluster is the PRIMARY
func (m *Manager) configureAsPrimary(ctx context.Context, failoverGroup *crdv1alpha1.FailoverGroup) error {
	log := m.Log.WithValues(
		"namespace", failoverGroup.Namespace,
		"name", failoverGroup.Name,
		"role", "PRIMARY",
	)

	// Here would be all the logic to:
	// 1. Scale up workloads
	// 2. Configure network resources
	// 3. Configure Flux resources
	// 4. Configure volume replications (as source)

	log.Info("Successfully configured as PRIMARY")
	return nil
}

// configureAsStandby configures resources when this cluster is a STANDBY
func (m *Manager) configureAsStandby(ctx context.Context, failoverGroup *crdv1alpha1.FailoverGroup) error {
	log := m.Log.WithValues(
		"namespace", failoverGroup.Namespace,
		"name", failoverGroup.Name,
		"role", "STANDBY",
	)

	// Here would be all the logic to:
	// 1. Scale down workloads
	// 2. Configure volume replications (as target)

	log.Info("Successfully configured as STANDBY")
	return nil
}

// GetFailoverGroup retrieves a FailoverGroup by name and namespace
func (m *Manager) GetFailoverGroup(ctx context.Context, namespace, name string) (*crdv1alpha1.FailoverGroup, error) {
	failoverGroup := &crdv1alpha1.FailoverGroup{}
	namespacedName := types.NamespacedName{
		Namespace: namespace,
		Name:      name,
	}
	if err := m.Client.Get(ctx, namespacedName, failoverGroup); err != nil {
		return nil, err
	}
	return failoverGroup, nil
}

// UpdateHeartbeat updates the heartbeat timestamp for this cluster in DynamoDB
func (m *Manager) UpdateHeartbeat(ctx context.Context, failoverGroup *crdv1alpha1.FailoverGroup) error {
	log := m.Log.WithValues(
		"namespace", failoverGroup.Namespace,
		"name", failoverGroup.Name,
	)

	if m.DynamoDBManager == nil {
		log.Info("DynamoDB manager not configured, skipping heartbeat update")
		return nil
	}

	// Update heartbeat in DynamoDB
	return m.DynamoDBManager.UpdateHeartbeat(
		ctx,
		failoverGroup.Namespace,
		failoverGroup.Name,
		m.ClusterName,
	)
}

// GetVolumeStateFromDynamoDB retrieves the volume state from DynamoDB
// This is used to coordinate failover stages between clusters
func (m *Manager) GetVolumeStateFromDynamoDB(ctx context.Context, failoverGroup *crdv1alpha1.FailoverGroup) (string, bool) {
	log := m.Log.WithValues(
		"namespace", failoverGroup.Namespace,
		"name", failoverGroup.Name,
	)

	if m.DynamoDBManager == nil {
		log.Info("DynamoDB manager not configured, cannot get volume state")
		return "", false
	}

	// Get volume state from DynamoDB
	volumeState, err := m.DynamoDBManager.GetVolumeState(
		ctx,
		failoverGroup.Namespace,
		failoverGroup.Name,
	)

	if err != nil {
		log.Error(err, "Failed to get volume state from DynamoDB")
		return "", false
	}

	if volumeState == "" {
		return "", false
	}

	return volumeState, true
}

// HandleVolumePromotion handles volume promotion during Stage 4 of failover
func (m *Manager) HandleVolumePromotion(ctx context.Context, failoverGroup *crdv1alpha1.FailoverGroup) error {
	log := m.Log.WithValues(
		"namespace", failoverGroup.Namespace,
		"name", failoverGroup.Name,
	)
	log.Info("Handling volume promotion (Stage 4)")

	// 1. Identify and promote volumes
	if err := m.promoteVolumes(ctx, failoverGroup); err != nil {
		return fmt.Errorf("failed to promote volumes: %w", err)
	}

	// 2. Wait for volumes to be fully promoted
	if err := m.waitForVolumesPromoted(ctx, failoverGroup); err != nil {
		return fmt.Errorf("failed to wait for volumes promotion: %w", err)
	}

	// 3. Update DynamoDB to indicate volumes are promoted
	if m.DynamoDBManager != nil {
		if err := m.DynamoDBManager.SetVolumeState(
			ctx,
			failoverGroup.Namespace,
			failoverGroup.Name,
			"PROMOTED",
		); err != nil {
			return fmt.Errorf("failed to update volume state in DynamoDB: %w", err)
		}
	}

	log.Info("Volume promotion completed successfully")
	return nil
}

// HandleTargetActivation handles target cluster activation during Stage 5 of failover
func (m *Manager) HandleTargetActivation(ctx context.Context, failoverGroup *crdv1alpha1.FailoverGroup) error {
	log := m.Log.WithValues(
		"namespace", failoverGroup.Namespace,
		"name", failoverGroup.Name,
	)
	log.Info("Handling target cluster activation (Stage 5)")

	// 1. Scale up workloads in target cluster
	if err := m.scaleUpWorkloads(ctx, failoverGroup); err != nil {
		return fmt.Errorf("failed to scale up workloads: %w", err)
	}

	// 2. Trigger Flux reconciliation if specified
	if err := m.triggerFluxReconciliation(ctx, failoverGroup); err != nil {
		return fmt.Errorf("failed to trigger Flux reconciliation: %w", err)
	}

	// 3. Wait for workloads to be ready
	if err := m.waitForWorkloadsReady(ctx, failoverGroup); err != nil {
		return fmt.Errorf("failed to wait for workloads to be ready: %w", err)
	}

	// 4. Update network resources
	if err := m.updateNetworkResources(ctx, failoverGroup); err != nil {
		return fmt.Errorf("failed to update network resources: %w", err)
	}

	// 5. Update DynamoDB to indicate activation is complete
	if m.DynamoDBManager != nil {
		if err := m.DynamoDBManager.SetVolumeState(
			ctx,
			failoverGroup.Namespace,
			failoverGroup.Name,
			"COMPLETED",
		); err != nil {
			return fmt.Errorf("failed to update state in DynamoDB: %w", err)
		}
	}

	log.Info("Target cluster activation completed successfully")
	return nil
}

// promoteVolumes promotes volumes in the target cluster to Primary
func (m *Manager) promoteVolumes(ctx context.Context, failoverGroup *crdv1alpha1.FailoverGroup) error {
	// Implement volume promotion logic
	return nil
}

// waitForVolumesPromoted waits for all volumes to be promoted
func (m *Manager) waitForVolumesPromoted(ctx context.Context, failoverGroup *crdv1alpha1.FailoverGroup) error {
	// Implement waiting logic
	return nil
}

// scaleUpWorkloads scales up workloads in the target cluster
func (m *Manager) scaleUpWorkloads(ctx context.Context, failoverGroup *crdv1alpha1.FailoverGroup) error {
	// Implement scale-up logic
	return nil
}

// triggerFluxReconciliation triggers reconciliation of Flux resources
func (m *Manager) triggerFluxReconciliation(ctx context.Context, failoverGroup *crdv1alpha1.FailoverGroup) error {
	// Implement Flux reconciliation logic
	return nil
}

// waitForWorkloadsReady waits for all workloads to be ready
func (m *Manager) waitForWorkloadsReady(ctx context.Context, failoverGroup *crdv1alpha1.FailoverGroup) error {
	// Implement waiting logic
	return nil
}

// updateNetworkResources updates network resources in the target cluster
func (m *Manager) updateNetworkResources(ctx context.Context, failoverGroup *crdv1alpha1.FailoverGroup) error {
	// Implement network resource updates
	return nil
}
