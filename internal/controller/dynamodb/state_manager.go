package dynamodb

import (
	"context"
	"fmt"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/log"
)

// StateManager provides higher-level functionality that combines multiple managers
// It's used to manage the overall state of FailoverGroups
type StateManager struct {
	*Manager
	configManager  *GroupConfigManager
	statusManager  *ClusterStatusManager
	historyManager *HistoryManager
}

// NewStateManager creates a new state manager
func NewStateManager(baseManager *Manager) *StateManager {
	return &StateManager{
		Manager:        baseManager,
		configManager:  NewGroupConfigManager(baseManager),
		statusManager:  NewClusterStatusManager(baseManager),
		historyManager: NewHistoryManager(baseManager),
	}
}

// GetGroupState retrieves the current state of a FailoverGroup
// This combines data from both the config and status records
func (s *StateManager) GetGroupState(ctx context.Context, namespace, name string) (*GroupState, error) {
	logger := log.FromContext(ctx).WithValues(
		"namespace", namespace,
		"name", name,
	)
	logger.V(1).Info("Getting group state")

	// Get the group configuration
	config, err := s.configManager.GetGroupConfig(ctx, namespace, name)
	if err != nil {
		return nil, fmt.Errorf("failed to get group config: %w", err)
	}

	// Get the status for the owner cluster
	status, err := s.statusManager.GetClusterStatus(ctx, namespace, name, config.OwnerCluster)
	if err != nil {
		// If we can't get the status, still return a state with what we know
		logger.Error(err, "Failed to get cluster status, returning partial state")
	}

	// Get the failover history count
	history, err := s.historyManager.GetFailoverHistory(ctx, namespace, name, 1)
	if err != nil {
		logger.Error(err, "Failed to get failover history")
	}

	var lastHeartbeat *time.Time
	var currentRole string
	var clusterHealth string

	if status != nil {
		t := status.LastHeartbeat
		lastHeartbeat = &t
		currentRole = status.State
		clusterHealth = status.Health
	} else {
		currentRole = StatePrimary // Default if we can't determine
		clusterHealth = HealthOK   // Default if we can't determine
	}

	var lastFailover *time.Time
	var failoverReason string
	var failoverCount int

	if config.LastFailover != nil {
		t := config.LastFailover.Timestamp
		lastFailover = &t
		failoverReason = "Latest failover reason not available"
	}

	if history != nil && len(history) > 0 {
		failoverCount = len(history)
		if lastFailover == nil {
			t := history[0].StartTime
			lastFailover = &t
		}
		failoverReason = history[0].Reason
	}

	return &GroupState{
		GroupID:        fmt.Sprintf("%s/%s", namespace, name),
		Status:         clusterHealth,
		CurrentRole:    currentRole,
		FailoverCount:  failoverCount,
		LastFailover:   lastFailover,
		LastHeartbeat:  lastHeartbeat,
		FailoverReason: failoverReason,
		LastUpdate:     time.Now().Unix(),
	}, nil
}

// UpdateState updates the state of a FailoverGroup
// This is primarily for testing and manual operations
func (s *StateManager) UpdateState(ctx context.Context, state *GroupState) error {
	logger := log.FromContext(ctx).WithValues(
		"groupID", state.GroupID,
	)
	logger.V(1).Info("Updating group state")

	// This is a placeholder implementation
	// In a real implementation, this would update multiple records:
	// - The group configuration
	// - The cluster status

	return fmt.Errorf("not implemented - this is for testing only")
}

// SyncClusterState synchronizes the state of this cluster with DynamoDB
// This is called periodically to ensure the cluster's state is up to date
func (s *StateManager) SyncClusterState(ctx context.Context, namespace, name string) error {
	logger := log.FromContext(ctx).WithValues(
		"namespace", namespace,
		"name", name,
		"clusterName", s.clusterName,
	)
	logger.V(1).Info("Synchronizing cluster state")

	// 1. Get the current group configuration
	config, err := s.configManager.GetGroupConfig(ctx, namespace, name)
	if err != nil {
		return fmt.Errorf("failed to get group config: %w", err)
	}

	// 2. Determine this cluster's role based on ownership
	var state string
	if config.OwnerCluster == s.clusterName {
		state = StatePrimary
	} else {
		state = StateStandby
	}

	// 3. Update this cluster's status
	// TODO: In the actual implementation, we'd get real component status
	components := make(map[string]ComponentStatus)
	components["database"] = ComponentStatus{
		Health:  HealthOK,
		Message: "Database is healthy",
	}
	components["application"] = ComponentStatus{
		Health:  HealthOK,
		Message: "Application is healthy",
	}

	// 4. Calculate the overall health based on component status
	health := HealthOK
	for _, compStatus := range components {
		if compStatus.Health == HealthError {
			health = HealthError
			break
		} else if compStatus.Health == HealthDegraded && health != HealthError {
			health = HealthDegraded
		}
	}

	// 5. Update the status record
	return s.statusManager.UpdateClusterStatus(ctx, namespace, name, health, state, components)
}

// DetectAndReportProblems detects problems with FailoverGroups
// This is used to identify issues that might require attention
func (s *StateManager) DetectAndReportProblems(ctx context.Context, namespace, name string) ([]string, error) {
	logger := log.FromContext(ctx).WithValues(
		"namespace", namespace,
		"name", name,
	)
	logger.V(1).Info("Detecting problems")

	var problems []string

	// 1. Check for stale heartbeats
	staleClusters, err := s.statusManager.DetectStaleHeartbeats(ctx, namespace, name)
	if err != nil {
		return nil, fmt.Errorf("failed to detect stale heartbeats: %w", err)
	}
	for _, cluster := range staleClusters {
		problems = append(problems, fmt.Sprintf("Stale heartbeat detected for cluster %s", cluster))
	}

	// 2. Check for unhealthy components in all clusters
	statuses, err := s.statusManager.GetAllClusterStatuses(ctx, namespace, name)
	if err != nil {
		return problems, fmt.Errorf("failed to get cluster statuses: %w", err)
	}

	for clusterName, status := range statuses {
		if status.Health == HealthError {
			problems = append(problems, fmt.Sprintf("Cluster %s is reporting ERROR health state", clusterName))
		} else if status.Health == HealthDegraded {
			problems = append(problems, fmt.Sprintf("Cluster %s is reporting DEGRADED health state", clusterName))
		}

		for componentName, componentStatus := range status.Components {
			if componentStatus.Health == HealthError {
				problems = append(problems, fmt.Sprintf("Component %s in cluster %s is reporting ERROR health state: %s",
					componentName, clusterName, componentStatus.Message))
			}
		}
	}

	return problems, nil
}
