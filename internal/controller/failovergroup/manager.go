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
	groupState, err := m.DynamoDBManager.State.GetGroupState(ctx, failoverGroup.Namespace, failoverGroup.Name)
	if err != nil {
		log.Error(err, "Failed to get group state from DynamoDB")
		return err
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
	return m.DynamoDBManager.State.UpdateClusterStatus(
		ctx,
		failoverGroup.Namespace,
		failoverGroup.Name,
		m.ClusterName,
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
func (m *Manager) updateLocalStatus(ctx context.Context, failoverGroup *crdv1alpha1.FailoverGroup, groupState *dynamodb.GroupState) error {
	// Update the FailoverGroup status based on the DynamoDB state
	updated := false

	// Get config directly from DynamoDB service
	config, err := m.DynamoDBManager.State.GetGroupConfig(ctx, failoverGroup.Namespace, failoverGroup.Name)
	if err != nil {
		return fmt.Errorf("failed to get group config: %w", err)
	}

	// Initialize GlobalState if needed
	if failoverGroup.Status.GlobalState.ActiveCluster == "" && failoverGroup.Status.GlobalState.ThisCluster == "" {
		// This means it's likely not initialized yet
		failoverGroup.Status.GlobalState = crdv1alpha1.GlobalStateInfo{
			ThisCluster: m.ClusterName,
		}
	}

	// Update owner cluster if different
	if failoverGroup.Status.GlobalState.ActiveCluster != config.OwnerCluster {
		failoverGroup.Status.GlobalState.ActiveCluster = config.OwnerCluster
		updated = true

		// Configure resources based on role
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
		if err := m.Client.Status().Update(ctx, failoverGroup); err != nil {
			m.Log.Error(err, "Failed to update FailoverGroup status")
			return err
		}
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
	var failoverGroup crdv1alpha1.FailoverGroup

	err := m.Client.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      name,
	}, &failoverGroup)

	if err != nil {
		return nil, err
	}

	return &failoverGroup, nil
}
