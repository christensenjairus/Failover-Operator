package dynamodb

import (
	"context"
	"fmt"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/log"
)

// TransactionManager handles complex transactions that involve multiple DynamoDB operations
type TransactionManager struct {
	*Manager
	lockManager    *LockManager
	configManager  *GroupConfigManager
	statusManager  *ClusterStatusManager
	historyManager *HistoryManager
}

// NewTransactionManager creates a new transaction manager
func NewTransactionManager(baseManager *Manager) *TransactionManager {
	return &TransactionManager{
		Manager:        baseManager,
		lockManager:    NewLockManager(baseManager),
		configManager:  NewGroupConfigManager(baseManager),
		statusManager:  NewClusterStatusManager(baseManager),
		historyManager: NewHistoryManager(baseManager),
	}
}

// ExecuteFailover executes a failover from one cluster to another
func (m *TransactionManager) ExecuteFailover(ctx context.Context, namespace, name, failoverName, targetCluster, reason string, forceFastMode bool) error {
	logger := log.FromContext(ctx).WithValues(
		"namespace", namespace,
		"name", name,
		"targetCluster", targetCluster,
		"forceFastMode", forceFastMode,
	)
	logger.V(1).Info("Executing failover")

	startTime := time.Now()

	// 1. Get the current configuration to determine the source cluster
	config, err := m.configManager.GetGroupConfig(ctx, namespace, name)
	if err != nil {
		return fmt.Errorf("failed to get current config: %w", err)
	}

	sourceCluster := config.OwnerCluster
	if sourceCluster == targetCluster {
		return fmt.Errorf("source and target clusters are the same: %s", sourceCluster)
	}

	// 2. Acquire a lock to prevent concurrent operations
	leaseToken, err := m.lockManager.AcquireLock(ctx, namespace, name, fmt.Sprintf("Failover to %s", targetCluster))
	if err != nil {
		return fmt.Errorf("failed to acquire lock: %w", err)
	}

	// Ensure we release the lock when we're done
	defer func() {
		if releaseErr := m.lockManager.ReleaseLock(ctx, namespace, name, leaseToken); releaseErr != nil {
			logger.Error(releaseErr, "Failed to release lock after failover")
		}
	}()

	// 3. Transfer ownership from source to target
	if err := m.configManager.TransferOwnership(ctx, namespace, name, targetCluster); err != nil {
		return fmt.Errorf("failed to transfer ownership: %w", err)
	}

	// 4. Record the failover event in history
	endTime := time.Now()
	if err := m.historyManager.RecordFailoverEvent(ctx, namespace, name, failoverName, sourceCluster, targetCluster, reason, startTime, endTime, "SUCCESS", &FailoverMetrics{
		TotalDowntimeSeconds:     int64(endTime.Sub(startTime).Seconds()),
		TotalFailoverTimeSeconds: int64(endTime.Sub(startTime).Seconds()),
	}); err != nil {
		logger.Error(err, "Failed to record failover event in history")
		// Continue despite this error, as the failover itself was successful
	}

	return nil
}

// ExecuteFailback executes a failback to the original primary cluster
func (m *TransactionManager) ExecuteFailback(ctx context.Context, namespace, name, reason string) error {
	logger := log.FromContext(ctx).WithValues(
		"namespace", namespace,
		"name", name,
	)
	logger.V(1).Info("Executing failback")

	// 1. Get the current configuration to determine the current and previous owners
	config, err := m.configManager.GetGroupConfig(ctx, namespace, name)
	if err != nil {
		return fmt.Errorf("failed to get current config: %w", err)
	}

	if config.PreviousOwner == "" {
		return fmt.Errorf("no previous owner to failback to")
	}

	// 2. Execute a failover to the previous owner
	return m.ExecuteFailover(ctx, namespace, name, fmt.Sprintf("failback-%s", time.Now().Format("20060102-150405")), config.PreviousOwner, reason, false)
}

// ValidateFailoverPreconditions validates that preconditions for a failover are met
func (m *TransactionManager) ValidateFailoverPreconditions(ctx context.Context, namespace, name, targetCluster string, skipHealthCheck bool) error {
	logger := log.FromContext(ctx).WithValues(
		"namespace", namespace,
		"name", name,
		"targetCluster", targetCluster,
		"skipHealthCheck", skipHealthCheck,
	)
	logger.V(1).Info("Validating failover preconditions")

	// 1. Check if the group is suspended
	suspended, reason, err := m.configManager.IsSuspended(ctx, namespace, name)
	if err != nil {
		return fmt.Errorf("failed to check suspension status: %w", err)
	}
	if suspended {
		return fmt.Errorf("failover group is suspended: %s", reason)
	}

	// 2. Check if a lock already exists
	locked, lockedBy, err := m.lockManager.IsLocked(ctx, namespace, name)
	if err != nil {
		return fmt.Errorf("failed to check lock status: %w", err)
	}
	if locked {
		return fmt.Errorf("failover group is locked by: %s", lockedBy)
	}

	// 3. Check target cluster health
	if !skipHealthCheck {
		healthy, err := m.statusManager.CheckClusterHealth(ctx, namespace, name, targetCluster)
		if err != nil {
			return fmt.Errorf("failed to check target cluster health: %w", err)
		}
		if !healthy {
			return fmt.Errorf("target cluster %s is not healthy", targetCluster)
		}
	}

	return nil
}
