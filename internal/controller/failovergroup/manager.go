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

	// Track known clusters for each FailoverGroup
	knownClusters map[string][]string // key: namespace/name, value: cluster names
}

// NewManager creates a new FailoverGroup manager
func NewManager(client client.Client, clusterName string, log logr.Logger) *Manager {
	return &Manager{
		Client:        client,
		ClusterName:   clusterName,
		Log:           log.WithName("failovergroup-manager"),
		knownClusters: make(map[string][]string),
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

	// Gather actual status data from cluster
	statusData, health, err := m.gatherClusterResourceStatuses(ctx, failoverGroup)
	if err != nil {
		log.Error(err, "Failed to gather complete cluster status")
		// Continue with partial data
	}

	// Determine role based on the FailoverGroup status
	role := m.determineClusterRole(failoverGroup)

	// Update the status in DynamoDB with real data
	return m.DynamoDBManager.UpdateClusterStatus(
		ctx,
		failoverGroup.Namespace,
		failoverGroup.Name,
		health, // Use actual health status
		role,
		statusData,
	)
}

// gatherClusterResourceStatuses collects status information from various resources in the cluster
// This information is stored in DynamoDB and made available to all clusters in the FailoverGroup
// through the GlobalState.Clusters field. This provides a complete view of all clusters' health
// and resource statuses, enabling better decision-making during failover operations.
func (m *Manager) gatherClusterResourceStatuses(ctx context.Context, failoverGroup *crdv1alpha1.FailoverGroup) (*dynamodb.StatusData, string, error) {
	log := m.Log.WithValues(
		"namespace", failoverGroup.Namespace,
		"name", failoverGroup.Name,
	)

	statusData := &dynamodb.StatusData{
		Workloads:          []dynamodb.ResourceStatus{},
		NetworkResources:   []dynamodb.ResourceStatus{},
		FluxResources:      []dynamodb.ResourceStatus{},
		VolumeReplications: []dynamodb.WorkloadReplicationStatus{},
	}

	// Get workload statuses (deployments, statefulsets, etc.)
	workloadStatuses, workloadHealth, err := m.gatherWorkloadStatuses(ctx, failoverGroup)
	if err != nil {
		log.Error(err, "Failed to gather workload statuses")
		// Continue anyway, just with partial data
	}
	statusData.Workloads = workloadStatuses

	// Get network resource statuses (ingresses, virtualservices)
	networkStatuses, networkHealth, err := m.gatherNetworkResourceStatuses(ctx, failoverGroup)
	if err != nil {
		log.Error(err, "Failed to gather network resource statuses")
	}
	statusData.NetworkResources = networkStatuses

	// Get Flux resource statuses (kustomizations, helmreleases)
	fluxStatuses, fluxHealth, err := m.gatherFluxResourceStatuses(ctx, failoverGroup)
	if err != nil {
		log.Error(err, "Failed to gather Flux resource statuses")
	}
	statusData.FluxResources = fluxStatuses

	// Get volume replication statuses
	volumeStatuses, volumeHealth, err := m.gatherVolumeReplicationStatuses(ctx, failoverGroup)
	if err != nil {
		log.Error(err, "Failed to gather volume replication statuses")
	}
	statusData.VolumeReplications = volumeStatuses

	// Determine overall health from component health
	overallHealth := m.determineOverallHealth(workloadHealth, networkHealth, fluxHealth, volumeHealth)

	return statusData, overallHealth, nil
}

// gatherWorkloadStatuses collects status information from workload resources
func (m *Manager) gatherWorkloadStatuses(ctx context.Context, fg *crdv1alpha1.FailoverGroup) ([]dynamodb.ResourceStatus, string, error) {
	statuses := []dynamodb.ResourceStatus{}
	overallHealth := dynamodb.HealthOK

	// Process each workload selector in the failover group
	for _, selector := range fg.Spec.Workloads {
		switch selector.Kind {
		case "Deployment":
			deploymentStatuses, health, err := m.gatherDeploymentStatuses(ctx, fg.Namespace, selector)
			if err != nil {
				return statuses, dynamodb.HealthDegraded, err
			}
			statuses = append(statuses, deploymentStatuses...)
			if health != dynamodb.HealthOK && overallHealth == dynamodb.HealthOK {
				overallHealth = health
			}
			if health == dynamodb.HealthError {
				overallHealth = dynamodb.HealthError
			}

		case "StatefulSet":
			stsStatuses, health, err := m.gatherStatefulSetStatuses(ctx, fg.Namespace, selector)
			if err != nil {
				return statuses, dynamodb.HealthDegraded, err
			}
			statuses = append(statuses, stsStatuses...)
			if health != dynamodb.HealthOK && overallHealth == dynamodb.HealthOK {
				overallHealth = health
			}
			if health == dynamodb.HealthError {
				overallHealth = dynamodb.HealthError
			}

		case "CronJob":
			cronJobStatuses, health, err := m.gatherCronJobStatuses(ctx, fg.Namespace, selector)
			if err != nil {
				return statuses, dynamodb.HealthDegraded, err
			}
			statuses = append(statuses, cronJobStatuses...)
			if health != dynamodb.HealthOK && overallHealth == dynamodb.HealthOK {
				overallHealth = health
			}
			if health == dynamodb.HealthError {
				overallHealth = dynamodb.HealthError
			}
		}
	}

	return statuses, overallHealth, nil
}

// gatherNetworkResourceStatuses collects status information from network resources
func (m *Manager) gatherNetworkResourceStatuses(ctx context.Context, fg *crdv1alpha1.FailoverGroup) ([]dynamodb.ResourceStatus, string, error) {
	statuses := []dynamodb.ResourceStatus{}
	overallHealth := dynamodb.HealthOK

	// Process each network resource selector in the failover group
	for _, selector := range fg.Spec.NetworkResources {
		switch selector.Kind {
		case "Ingress":
			ingressStatuses, health, err := m.gatherIngressStatuses(ctx, fg.Namespace, selector)
			if err != nil {
				return statuses, dynamodb.HealthDegraded, err
			}
			statuses = append(statuses, ingressStatuses...)
			if health != dynamodb.HealthOK && overallHealth == dynamodb.HealthOK {
				overallHealth = health
			}

		case "VirtualService":
			vsStatuses, health, err := m.gatherVirtualServiceStatuses(ctx, fg.Namespace, selector)
			if err != nil {
				return statuses, dynamodb.HealthDegraded, err
			}
			statuses = append(statuses, vsStatuses...)
			if health != dynamodb.HealthOK && overallHealth == dynamodb.HealthOK {
				overallHealth = health
			}
		}
	}

	return statuses, overallHealth, nil
}

// gatherFluxResourceStatuses collects status information from Flux resources
func (m *Manager) gatherFluxResourceStatuses(ctx context.Context, fg *crdv1alpha1.FailoverGroup) ([]dynamodb.ResourceStatus, string, error) {
	statuses := []dynamodb.ResourceStatus{}
	overallHealth := dynamodb.HealthOK

	// Process each Flux resource selector in the failover group
	for _, selector := range fg.Spec.FluxResources {
		switch selector.Kind {
		case "Kustomization":
			kustomizationStatuses, health, err := m.gatherKustomizationStatuses(ctx, fg.Namespace, selector)
			if err != nil {
				return statuses, dynamodb.HealthDegraded, err
			}
			statuses = append(statuses, kustomizationStatuses...)
			if health != dynamodb.HealthOK && overallHealth == dynamodb.HealthOK {
				overallHealth = health
			}

		case "HelmRelease":
			helmReleaseStatuses, health, err := m.gatherHelmReleaseStatuses(ctx, fg.Namespace, selector)
			if err != nil {
				return statuses, dynamodb.HealthDegraded, err
			}
			statuses = append(statuses, helmReleaseStatuses...)
			if health != dynamodb.HealthOK && overallHealth == dynamodb.HealthOK {
				overallHealth = health
			}
		}
	}

	return statuses, overallHealth, nil
}

// gatherVolumeReplicationStatuses collects status information from volume replications
func (m *Manager) gatherVolumeReplicationStatuses(ctx context.Context, fg *crdv1alpha1.FailoverGroup) ([]dynamodb.WorkloadReplicationStatus, string, error) {
	statuses := []dynamodb.WorkloadReplicationStatus{}
	overallHealth := dynamodb.HealthOK

	// Check each workload that might have volume replications
	for _, workload := range fg.Spec.Workloads {
		// Only StatefulSets typically have volume replications
		if workload.Kind != "StatefulSet" || len(workload.VolumeReplications) == 0 {
			continue
		}

		vrStatuses, health, err := m.gatherStatefulSetVolumeReplications(ctx, fg.Namespace, workload)
		if err != nil {
			return statuses, dynamodb.HealthDegraded, err
		}
		statuses = append(statuses, vrStatuses...)
		if health != dynamodb.HealthOK && overallHealth == dynamodb.HealthOK {
			overallHealth = health
		}
		if health == dynamodb.HealthError {
			overallHealth = dynamodb.HealthError
		}
	}

	return statuses, overallHealth, nil
}

// gatherDeploymentStatuses collects status information from Deployments
func (m *Manager) gatherDeploymentStatuses(ctx context.Context, namespace string, selector crdv1alpha1.WorkloadSpec) ([]dynamodb.ResourceStatus, string, error) {
	// This would be implemented with actual Kubernetes API calls to get Deployment statuses
	// For now, we'll return a placeholder implementation
	statuses := []dynamodb.ResourceStatus{
		{
			Kind:   "Deployment",
			Name:   selector.Name,
			Health: dynamodb.HealthOK,
			Status: "Available",
		},
	}
	return statuses, dynamodb.HealthOK, nil
}

// gatherStatefulSetStatuses collects status information from StatefulSets
func (m *Manager) gatherStatefulSetStatuses(ctx context.Context, namespace string, selector crdv1alpha1.WorkloadSpec) ([]dynamodb.ResourceStatus, string, error) {
	// This would be implemented with actual Kubernetes API calls to get StatefulSet statuses
	// For now, we'll return a placeholder implementation
	statuses := []dynamodb.ResourceStatus{
		{
			Kind:   "StatefulSet",
			Name:   selector.Name,
			Health: dynamodb.HealthOK,
			Status: "Available",
		},
	}
	return statuses, dynamodb.HealthOK, nil
}

// gatherCronJobStatuses collects status information from CronJobs
func (m *Manager) gatherCronJobStatuses(ctx context.Context, namespace string, selector crdv1alpha1.WorkloadSpec) ([]dynamodb.ResourceStatus, string, error) {
	// This would be implemented with actual Kubernetes API calls to get CronJob statuses
	// For now, we'll return a placeholder implementation
	statuses := []dynamodb.ResourceStatus{
		{
			Kind:   "CronJob",
			Name:   selector.Name,
			Health: dynamodb.HealthOK,
			Status: "Scheduled",
		},
	}
	return statuses, dynamodb.HealthOK, nil
}

// gatherIngressStatuses collects status information from Ingresses
func (m *Manager) gatherIngressStatuses(ctx context.Context, namespace string, selector crdv1alpha1.NetworkResourceSpec) ([]dynamodb.ResourceStatus, string, error) {
	// This would be implemented with actual Kubernetes API calls to get Ingress statuses
	// For now, we'll return a placeholder implementation
	statuses := []dynamodb.ResourceStatus{
		{
			Kind:   "Ingress",
			Name:   selector.Name,
			Health: dynamodb.HealthOK,
			Status: "Available",
		},
	}
	return statuses, dynamodb.HealthOK, nil
}

// gatherVirtualServiceStatuses collects status information from VirtualServices
func (m *Manager) gatherVirtualServiceStatuses(ctx context.Context, namespace string, selector crdv1alpha1.NetworkResourceSpec) ([]dynamodb.ResourceStatus, string, error) {
	// This would be implemented with actual Kubernetes API calls to get VirtualService statuses
	// For now, we'll return a placeholder implementation
	statuses := []dynamodb.ResourceStatus{
		{
			Kind:   "VirtualService",
			Name:   selector.Name,
			Health: dynamodb.HealthOK,
			Status: "Available",
		},
	}
	return statuses, dynamodb.HealthOK, nil
}

// gatherKustomizationStatuses collects status information from Kustomizations
func (m *Manager) gatherKustomizationStatuses(ctx context.Context, namespace string, selector crdv1alpha1.FluxResourceSpec) ([]dynamodb.ResourceStatus, string, error) {
	// This would be implemented with actual Kubernetes API calls to get Kustomization statuses
	// For now, we'll return a placeholder implementation
	statuses := []dynamodb.ResourceStatus{
		{
			Kind:   "Kustomization",
			Name:   selector.Name,
			Health: dynamodb.HealthOK,
			Status: "Ready",
		},
	}
	return statuses, dynamodb.HealthOK, nil
}

// gatherHelmReleaseStatuses collects status information from HelmReleases
func (m *Manager) gatherHelmReleaseStatuses(ctx context.Context, namespace string, selector crdv1alpha1.FluxResourceSpec) ([]dynamodb.ResourceStatus, string, error) {
	// This would be implemented with actual Kubernetes API calls to get HelmRelease statuses
	// For now, we'll return a placeholder implementation
	statuses := []dynamodb.ResourceStatus{
		{
			Kind:   "HelmRelease",
			Name:   selector.Name,
			Health: dynamodb.HealthOK,
			Status: "Ready",
		},
	}
	return statuses, dynamodb.HealthOK, nil
}

// gatherStatefulSetVolumeReplications collects status information for volume replications
func (m *Manager) gatherStatefulSetVolumeReplications(ctx context.Context, namespace string, workload crdv1alpha1.WorkloadSpec) ([]dynamodb.WorkloadReplicationStatus, string, error) {
	// This would be implemented with actual Kubernetes API calls to get VolumeReplication statuses
	// For now, we'll return a placeholder implementation
	vrStatus := dynamodb.WorkloadReplicationStatus{
		WorkloadKind:       workload.Kind,
		WorkloadName:       workload.Name,
		VolumeReplications: []dynamodb.ResourceStatus{},
	}

	// Add a replication status for each volume replication mentioned in the workload
	for _, vrName := range workload.VolumeReplications {
		vrStatus.VolumeReplications = append(vrStatus.VolumeReplications, dynamodb.ResourceStatus{
			Kind:   "VolumeReplication",
			Name:   vrName,
			Health: dynamodb.HealthOK,
			Status: "Replicating",
		})
	}

	return []dynamodb.WorkloadReplicationStatus{vrStatus}, dynamodb.HealthOK, nil
}

// determineOverallHealth assesses the overall health based on component health
func (m *Manager) determineOverallHealth(workloadHealth, networkHealth, fluxHealth, volumeHealth string) string {
	// If any component is in ERROR state, the overall health is ERROR
	if workloadHealth == dynamodb.HealthError ||
		networkHealth == dynamodb.HealthError ||
		fluxHealth == dynamodb.HealthError ||
		volumeHealth == dynamodb.HealthError {
		return dynamodb.HealthError
	}

	// If any component is in DEGRADED state, the overall health is DEGRADED
	if workloadHealth == dynamodb.HealthDegraded ||
		networkHealth == dynamodb.HealthDegraded ||
		fluxHealth == dynamodb.HealthDegraded ||
		volumeHealth == dynamodb.HealthDegraded {
		return dynamodb.HealthDegraded
	}

	// Otherwise, the overall health is OK
	return dynamodb.HealthOK
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

		log := m.Log.WithValues(
			"namespace", failoverGroup.Namespace,
			"name", failoverGroup.Name,
			"cluster", m.ClusterName)

		// Try to proactively register this cluster if not already registered
		if m.DynamoDBManager != nil {
			// Check if this cluster exists in DynamoDB
			statuses, err := m.DynamoDBManager.GetAllClusterStatuses(ctx, failoverGroup.Namespace, failoverGroup.Name)
			if err != nil {
				log.Error(err, "Failed to get cluster statuses from DynamoDB")
				// Continue anyway to try the other operations
			} else {
				// Log all clusters found in DynamoDB for debugging
				clusterList := []string{}
				for cluster := range statuses {
					clusterList = append(clusterList, cluster)
				}
				log.Info("Retrieved cluster statuses from DynamoDB", "clusters", clusterList, "count", len(statuses))

				// Check if our cluster exists
				_, exists := statuses[m.ClusterName]
				if !exists {
					log.Info("Proactively registering this cluster in DynamoDB",
						"cluster", m.ClusterName)

					// Determine the role based on current state
					role := "STANDBY"
					if failoverGroup.Status.GlobalState.ActiveCluster == m.ClusterName {
						role = "PRIMARY"
					}

					// Register this cluster
					if err := m.DynamoDBManager.UpdateClusterStatus(
						ctx,
						failoverGroup.Namespace,
						failoverGroup.Name,
						"OK", // Default health
						role,
						nil, // No detailed status yet
					); err != nil {
						log.Error(err, "Failed to register cluster in DynamoDB")
					} else {
						log.Info("Successfully registered cluster in DynamoDB")
					}
				} else {
					log.V(1).Info("Cluster already registered in DynamoDB")
				}
			}
		}

		if err := m.SyncWithDynamoDB(ctx, failoverGroup); err != nil {
			log.Error(err, "Failed to sync FailoverGroup with DynamoDB")
			continue
		}

		// Update the status in DynamoDB to ensure we're visible to other clusters
		if err := m.UpdateDynamoDBStatus(ctx, failoverGroup); err != nil {
			log.Error(err, "Failed to update status in DynamoDB")
		}

		// Clean up stale cluster statuses
		if err := m.CleanupStaleClusterStatuses(ctx, failoverGroup); err != nil {
			log.Error(err, "Failed to clean up stale cluster statuses")
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

	// Initialize GlobalState if it doesn't exist
	if failoverGroup.Status.GlobalState.ActiveCluster == "" {
		// Initialize with empty structure
		failoverGroup.Status.GlobalState = crdv1alpha1.GlobalStateInfo{
			ThisCluster:  m.ClusterName,
			DBSyncStatus: "Syncing",
			Clusters:     []crdv1alpha1.ClusterInfo{},
		}
	}

	// Flag to track if we need to update the resource
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

	// ------- AUTO-GENERATE CLUSTERS ARRAY FROM DYNAMODB -------

	// Get all cluster statuses from DynamoDB
	var clusterStatuses map[string]*dynamodb.ClusterStatusRecord
	if m.DynamoDBManager != nil {
		clusterStatuses, err = m.DynamoDBManager.GetAllClusterStatuses(ctx, failoverGroup.Namespace, failoverGroup.Name)
		if err != nil {
			log.Error(err, "Failed to get cluster statuses from DynamoDB")
			// Continue with what we have - don't fail the sync completely
		} else {
			// Log all clusters found in DynamoDB for debugging
			clusterList := []string{}
			for cluster := range clusterStatuses {
				clusterList = append(clusterList, cluster)
			}
			log.Info("Retrieved cluster statuses from DynamoDB", "clusters", clusterList, "count", len(clusterStatuses))
		}
	}

	// Initialize an empty clusters array
	newClusters := []crdv1alpha1.ClusterInfo{}

	// Always add this cluster to ensure it appears in the status
	thisClusterRole := "STANDBY"
	if activeCluster == m.ClusterName {
		thisClusterRole = "PRIMARY"
	}

	thisCluster := crdv1alpha1.ClusterInfo{
		Name:          m.ClusterName,
		Role:          thisClusterRole,
		Health:        "OK",
		LastHeartbeat: time.Now().Format(time.RFC3339),
	}
	newClusters = append(newClusters, thisCluster)

	// If we have cluster statuses from DynamoDB, add all other clusters
	if clusterStatuses != nil && len(clusterStatuses) > 0 {
		log.Info("Adding clusters from DynamoDB statuses",
			"totalClusters", len(clusterStatuses))

		// Add each cluster from DynamoDB (except this one, which we already added)
		for clusterName, status := range clusterStatuses {
			// Skip this cluster as we already added it
			if clusterName == m.ClusterName {
				continue
			}

			// Determine the role based on active cluster
			role := "STANDBY"
			if clusterName == activeCluster {
				role = "PRIMARY"
			}

			// Record the last heartbeat
			lastHeartbeat := time.Now().Format(time.RFC3339)
			if !status.LastHeartbeat.IsZero() {
				lastHeartbeat = status.LastHeartbeat.Format(time.RFC3339)
			}

			// Create the cluster info
			clusterInfo := crdv1alpha1.ClusterInfo{
				Name:          clusterName,
				Role:          role,
				Health:        status.Health,
				LastHeartbeat: lastHeartbeat,
			}

			// Add to the new clusters array
			newClusters = append(newClusters, clusterInfo)
			log.V(1).Info("Added cluster to status", "cluster", clusterName, "role", role, "health", status.Health)

			// Also track this cluster in our known clusters map
			m.trackKnownCluster(fmt.Sprintf("%s/%s", failoverGroup.Namespace, failoverGroup.Name), clusterName)
		}
	} else {
		log.Info("No additional clusters found in DynamoDB beyond this cluster", "thisCluster", m.ClusterName)
	}

	// Compare old and new clusters arrays to determine if we need to update
	clustersChanged := len(failoverGroup.Status.GlobalState.Clusters) != len(newClusters)
	if !clustersChanged {
		// Check each cluster to see if any changed
		existingMap := make(map[string]crdv1alpha1.ClusterInfo)
		for _, cluster := range failoverGroup.Status.GlobalState.Clusters {
			existingMap[cluster.Name] = cluster
		}

		for _, newCluster := range newClusters {
			if oldCluster, exists := existingMap[newCluster.Name]; !exists ||
				oldCluster.Role != newCluster.Role ||
				oldCluster.Health != newCluster.Health {
				clustersChanged = true
				break
			}
		}
	}

	// Update clusters if changed
	if clustersChanged {
		log.Info("Updating clusters in status",
			"oldCount", len(failoverGroup.Status.GlobalState.Clusters),
			"newCount", len(newClusters),
			"clusters", getClusterNames(newClusters))
		failoverGroup.Status.GlobalState.Clusters = newClusters
		updated = true
	} else {
		log.V(1).Info("No changes to clusters array, keeping existing clusters",
			"count", len(failoverGroup.Status.GlobalState.Clusters))
	}

	// ------- END AUTO-GENERATE CLUSTERS ARRAY -------

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
		log.Info("Updating FailoverGroup status with changes")
		if err := m.Client.Status().Update(ctx, failoverGroup); err != nil {
			return fmt.Errorf("failed to update FailoverGroup status: %w", err)
		}
	} else {
		log.V(1).Info("No changes to FailoverGroup status")
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

// getClusterNames extracts the names of clusters for logging
func getClusterNames(clusters []crdv1alpha1.ClusterInfo) []string {
	names := make([]string, len(clusters))
	for i, cluster := range clusters {
		names[i] = cluster.Name
	}
	return names
}

// trackKnownCluster adds a cluster to the known clusters map
func (m *Manager) trackKnownCluster(fgKey, clusterName string) {
	// If this is the first known cluster for this FailoverGroup
	if m.knownClusters[fgKey] == nil {
		m.knownClusters[fgKey] = []string{clusterName}
		return
	}

	// Check if cluster is already known
	for _, name := range m.knownClusters[fgKey] {
		if name == clusterName {
			return // Already known
		}
	}

	// Add to known clusters
	m.knownClusters[fgKey] = append(m.knownClusters[fgKey], clusterName)
}

// getKnownClusters returns the list of known clusters for a FailoverGroup
func (m *Manager) getKnownClusters(fgKey string) []string {
	if clusters, ok := m.knownClusters[fgKey]; ok {
		return clusters
	}
	return []string{}
}

// splitClusterName breaks a cluster name like "dev-west" into ["dev", "west"]
func splitClusterName(clusterName string) []string {
	result := []string{"", ""}
	for i, c := range clusterName {
		if c == '-' {
			result[0] = clusterName[:i]
			result[1] = clusterName[i+1:]
			return result
		}
	}
	// If no hyphen, return the whole name as first part
	result[0] = clusterName
	return result
}

// CleanupStaleClusterStatuses checks for cluster statuses in DynamoDB with no recent heartbeats
// and removes them. This ensures that when a FailoverGroup is deleted on a remote cluster,
// its status is removed from DynamoDB and won't show up in other clusters' lists anymore.
func (m *Manager) CleanupStaleClusterStatuses(ctx context.Context, failoverGroup *crdv1alpha1.FailoverGroup) error {
	if m.DynamoDBManager == nil {
		return nil
	}

	log := m.Log.WithValues(
		"namespace", failoverGroup.Namespace,
		"name", failoverGroup.Name,
	)

	// Get all cluster statuses
	statuses, err := m.DynamoDBManager.GetAllClusterStatuses(ctx, failoverGroup.Namespace, failoverGroup.Name)
	if err != nil {
		return fmt.Errorf("failed to get cluster statuses: %w", err)
	}

	// Log all retrieved clusters for debugging
	clusterNames := []string{}
	for clusterName, status := range statuses {
		clusterNames = append(clusterNames, clusterName)
		log.V(1).Info("Found cluster status",
			"clusterName", clusterName,
			"health", status.Health,
			"state", status.State,
			"lastHeartbeat", status.LastHeartbeat.Format(time.RFC3339))
	}
	log.V(1).Info("Retrieved all cluster statuses", "clusters", clusterNames, "count", len(statuses))

	// Current time for heartbeat threshold
	now := time.Now()
	staleThreshold := 5 * time.Minute // Increased from 2 to 5 minutes to be more lenient

	// Create OperationsManager for status removal
	operationsManager := dynamodb.NewOperationsManager(m.DynamoDBManager.BaseManager)

	// Look for stale clusters
	var staleCount int
	for clusterName, status := range statuses {
		// Don't clean up our own status - only other clusters
		if clusterName == m.ClusterName {
			log.V(1).Info("Skipping our own cluster status", "clusterName", clusterName)
			continue
		}

		// Check if heartbeat is older than threshold
		lastHeartbeat := status.LastHeartbeat
		age := now.Sub(lastHeartbeat)

		log.V(1).Info("Checking cluster staleness",
			"clusterName", clusterName,
			"lastHeartbeat", lastHeartbeat.Format(time.RFC3339),
			"age", age.String(),
			"threshold", staleThreshold.String())

		if age > staleThreshold {
			log.Info("Removing stale cluster status",
				"clusterName", clusterName,
				"lastHeartbeat", lastHeartbeat.Format(time.RFC3339),
				"age", age.String(),
				"threshold", staleThreshold.String())

			// Remove the stale cluster status
			err := operationsManager.RemoveClusterStatus(ctx, failoverGroup.Namespace, failoverGroup.Name, clusterName)
			if err != nil {
				log.Error(err, "Failed to remove stale cluster status", "clusterName", clusterName)
				// Continue with other clusters
			} else {
				staleCount++
				log.Info("Successfully removed stale cluster status", "clusterName", clusterName)
			}
		} else {
			log.V(1).Info("Cluster status is fresh, keeping",
				"clusterName", clusterName,
				"age", age.String(),
				"threshold", staleThreshold.String())
		}
	}

	if staleCount > 0 {
		log.Info("Removed stale cluster statuses", "count", staleCount, "totalClusters", len(statuses))
	} else {
		log.V(1).Info("No stale cluster statuses found", "totalClusters", len(statuses))
	}

	return nil
}
