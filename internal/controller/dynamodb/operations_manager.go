package dynamodb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/google/uuid"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// OperationsManager provides functionality for complex operations like failovers,
// distributed locking, and transactional state changes
type OperationsManager struct {
	*BaseManager
}

// NewOperationsManager creates a new operations manager
func NewOperationsManager(baseManager *BaseManager) *OperationsManager {
	return &OperationsManager{
		BaseManager: baseManager,
	}
}

// ExecuteFailover executes a failover from one cluster to another
func (m *OperationsManager) ExecuteFailover(ctx context.Context, namespace, name, failoverName, targetCluster, reason string, forceFastMode bool) error {
	logger := log.FromContext(ctx).WithValues(
		"namespace", namespace,
		"name", name,
		"targetCluster", targetCluster,
		"forceFastMode", forceFastMode,
	)
	logger.Info("Executing failover to target cluster", "targetCluster", targetCluster)

	startTime := time.Now()

	// 1. Validate preconditions before proceeding
	if err := m.ValidateFailoverPreconditions(ctx, namespace, name, targetCluster, forceFastMode); err != nil {
		return fmt.Errorf("failover preconditions not met: %w", err)
	}

	// 2. Get the current configuration to determine the source cluster
	config, err := m.GetGroupConfig(ctx, namespace, name)
	if err != nil {
		return fmt.Errorf("failed to get current config: %w", err)
	}

	sourceCluster := config.OwnerCluster
	if sourceCluster == targetCluster {
		return fmt.Errorf("source and target clusters are the same: %s", sourceCluster)
	}

	// 3. Acquire a lock to prevent concurrent operations
	leaseToken, err := m.AcquireLock(ctx, namespace, name, fmt.Sprintf("Failover to %s", targetCluster))
	if err != nil {
		return fmt.Errorf("failed to acquire lock: %w", err)
	}

	// Use defer to ensure lock is released even on error
	defer func() {
		// Always try to release the lock, even if the failover fails
		if releaseErr := m.ReleaseLock(ctx, namespace, name, leaseToken); releaseErr != nil {
			logger.Error(releaseErr, "Failed to release lock after failover")
		}
	}()

	// 4. Use a transaction to update all relevant records atomically
	err = m.executeFailoverTransaction(ctx, namespace, name, sourceCluster, targetCluster, reason, failoverName, startTime)
	if err != nil {
		return fmt.Errorf("failover transaction failed: %w", err)
	}

	// 5. Record the successful failover
	endTime := time.Now()
	duration := int64(endTime.Sub(startTime).Seconds())

	// Approximate the "downtime" - in reality this depends on how quickly services switch over
	// We'll estimate it as half the operation time for now, but this can be refined
	downtime := duration / 2

	if err := m.recordFailoverEvent(ctx, namespace, name, failoverName, sourceCluster, targetCluster, reason, startTime, endTime, "SUCCESS", downtime, duration); err != nil {
		logger.Error(err, "Failed to record failover event in history", "error", err)
		// Continue despite this error, as the failover itself was successful
	}

	logger.Info("Failover executed successfully",
		"sourceCluster", sourceCluster,
		"targetCluster", targetCluster,
		"duration", fmt.Sprintf("%ds", duration))

	return nil
}

// executeFailoverTransaction performs the actual failover as a single atomic transaction
func (m *OperationsManager) executeFailoverTransaction(ctx context.Context, namespace, name, sourceCluster, targetCluster, reason, failoverName string, startTime time.Time) error {
	logger := log.FromContext(ctx)

	// Get DynamoDB primary keys
	pk := m.getGroupPK(namespace, name)
	configSK := "CONFIG"
	sourceSK := m.getClusterSK(sourceCluster)
	targetSK := m.getClusterSK(targetCluster)

	// Get current values to ensure conditional updates work correctly
	config, err := m.GetGroupConfig(ctx, namespace, name)
	if err != nil {
		return fmt.Errorf("failed to get current config for transaction: %w", err)
	}

	// Create transaction items
	var transactItems []types.TransactWriteItem

	// 1. Update group config to change ownership with condition check
	// Condition: current owner must be sourceCluster and version must match
	updateConfigExpr := "SET ownerCluster = :targetCluster, previousOwner = :sourceCluster, version = :newVersion, lastUpdated = :timestamp"
	configCondition := "ownerCluster = :sourceCluster AND version = :currentVersion"

	if config.LastFailover == nil {
		updateConfigExpr += ", lastFailover = :failoverRef"
	} else {
		updateConfigExpr += ", lastFailover.#ts = :failoverTimestamp, lastFailover.#nm = :failoverName, lastFailover.#ns = :namespace"
	}

	failoverRefJSON, _ := json.Marshal(FailoverReference{
		Name:      failoverName,
		Namespace: namespace,
		Timestamp: startTime,
	})

	configItem := types.TransactWriteItem{
		Update: &types.Update{
			TableName: &m.tableName,
			Key: map[string]types.AttributeValue{
				"PK": &types.AttributeValueMemberS{Value: pk},
				"SK": &types.AttributeValueMemberS{Value: configSK},
			},
			UpdateExpression:    aws.String(updateConfigExpr),
			ConditionExpression: aws.String(configCondition),
			ExpressionAttributeNames: map[string]string{
				"#ts": "timestamp",
				"#nm": "name",
				"#ns": "namespace",
			},
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":targetCluster":     &types.AttributeValueMemberS{Value: targetCluster},
				":sourceCluster":     &types.AttributeValueMemberS{Value: sourceCluster},
				":currentVersion":    &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", config.Version)},
				":newVersion":        &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", config.Version+1)},
				":timestamp":         &types.AttributeValueMemberS{Value: time.Now().Format(time.RFC3339)},
				":failoverRef":       &types.AttributeValueMemberS{Value: string(failoverRefJSON)},
				":failoverTimestamp": &types.AttributeValueMemberS{Value: startTime.Format(time.RFC3339)},
				":failoverName":      &types.AttributeValueMemberS{Value: failoverName},
				":namespace":         &types.AttributeValueMemberS{Value: namespace},
			},
		},
	}
	transactItems = append(transactItems, configItem)

	// 2. Update source cluster status to FAILOVER
	sourceItem := types.TransactWriteItem{
		Update: &types.Update{
			TableName: &m.tableName,
			Key: map[string]types.AttributeValue{
				"PK": &types.AttributeValueMemberS{Value: pk},
				"SK": &types.AttributeValueMemberS{Value: sourceSK},
			},
			UpdateExpression: aws.String("SET #state = :failoverState, lastHeartbeat = :timestamp"),
			ExpressionAttributeNames: map[string]string{
				"#state": "state",
			},
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":failoverState": &types.AttributeValueMemberS{Value: StateFailover},
				":timestamp":     &types.AttributeValueMemberS{Value: time.Now().Format(time.RFC3339)},
			},
		},
	}
	transactItems = append(transactItems, sourceItem)

	// 3. Update target cluster status to PRIMARY (or FAILBACK if coming back to original)
	targetItem := types.TransactWriteItem{
		Update: &types.Update{
			TableName: &m.tableName,
			Key: map[string]types.AttributeValue{
				"PK": &types.AttributeValueMemberS{Value: pk},
				"SK": &types.AttributeValueMemberS{Value: targetSK},
			},
			UpdateExpression: aws.String("SET #state = :primaryState, lastHeartbeat = :timestamp"),
			ExpressionAttributeNames: map[string]string{
				"#state": "state",
			},
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":primaryState": &types.AttributeValueMemberS{Value: StatePrimary},
				":timestamp":    &types.AttributeValueMemberS{Value: time.Now().Format(time.RFC3339)},
			},
		},
	}
	transactItems = append(transactItems, targetItem)

	// Execute the transaction
	_, err = m.client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: transactItems,
	})

	if err != nil {
		// Handle different types of transaction errors
		var txnCanceledException *types.TransactionCanceledException
		if errors.As(err, &txnCanceledException) {
			// Check specific reasons for transaction failure
			reasons := txnCanceledException.CancellationReasons

			// Log detailed error information for each item
			for i, reason := range reasons {
				if reason.Code != nil {
					itemDesc := "unknown"
					if i == 0 {
						itemDesc = "group config"
					} else if i == 1 {
						itemDesc = "source cluster"
					} else if i == 2 {
						itemDesc = "target cluster"
					}

					logger.Error(nil, "Transaction item failed",
						"item", itemDesc,
						"code", *reason.Code,
						"message", aws.ToString(reason.Message))
				}
			}

			return fmt.Errorf("failover transaction cancelled: %w", err)
		}

		return fmt.Errorf("failed to execute failover transaction: %w", err)
	}

	return nil
}

// ExecuteFailback executes a failback to the original primary cluster
func (m *OperationsManager) ExecuteFailback(ctx context.Context, namespace, name, reason string) error {
	logger := log.FromContext(ctx).WithValues(
		"namespace", namespace,
		"name", name,
	)
	logger.Info("Executing failback operation")

	startTime := time.Now()

	// 1. Get the current configuration to determine the current and previous owners
	config, err := m.GetGroupConfig(ctx, namespace, name)
	if err != nil {
		return fmt.Errorf("failed to get current config: %w", err)
	}

	if config.PreviousOwner == "" {
		return fmt.Errorf("cannot failback: no previous owner recorded")
	}

	sourceCluster := config.OwnerCluster  // Current owner
	targetCluster := config.PreviousOwner // The cluster we're failing back to
	failoverName := fmt.Sprintf("failback-%s", time.Now().Format("20060102-150405"))

	// 2. Validate prerequisites
	if err := m.ValidateFailoverPreconditions(ctx, namespace, name, targetCluster, false); err != nil {
		return fmt.Errorf("failback preconditions not met: %w", err)
	}

	// 3. Acquire a lock to prevent concurrent operations
	leaseToken, err := m.AcquireLock(ctx, namespace, name, fmt.Sprintf("Failback to %s", targetCluster))
	if err != nil {
		return fmt.Errorf("failed to acquire lock for failback: %w", err)
	}

	// Use defer to ensure lock is released even on error
	defer func() {
		// Always try to release the lock, even if the failover fails
		if releaseErr := m.ReleaseLock(ctx, namespace, name, leaseToken); releaseErr != nil {
			logger.Error(releaseErr, "Failed to release lock after failback")
		}
	}()

	// 4. Execute the failback transaction (similar to failover but with special handling)
	err = m.executeFailbackTransaction(ctx, namespace, name, sourceCluster, targetCluster, reason, failoverName, startTime)
	if err != nil {
		return fmt.Errorf("failback transaction failed: %w", err)
	}

	// 5. Record the successful failback
	endTime := time.Now()
	duration := int64(endTime.Sub(startTime).Seconds())

	// Approximate the "downtime" - for failback we might expect it to be shorter
	// but still depends on how quickly services switch
	downtime := duration / 3 // Estimate less downtime for failback than failover

	if err := m.recordFailoverEvent(ctx, namespace, name, failoverName, sourceCluster, targetCluster, reason, startTime, endTime, "SUCCESS", downtime, duration); err != nil {
		logger.Error(err, "Failed to record failback event in history", "error", err)
		// Continue despite this error, as the failback itself was successful
	}

	logger.Info("Failback executed successfully",
		"sourceCluster", sourceCluster,
		"targetCluster", targetCluster,
		"duration", fmt.Sprintf("%ds", duration))

	return nil
}

// executeFailbackTransaction performs the actual failback as a single atomic transaction
func (m *OperationsManager) executeFailbackTransaction(ctx context.Context, namespace, name, sourceCluster, targetCluster, reason, failbackName string, startTime time.Time) error {
	logger := log.FromContext(ctx)

	// Get DynamoDB primary keys
	pk := m.getGroupPK(namespace, name)
	configSK := "CONFIG"
	sourceSK := m.getClusterSK(sourceCluster)
	targetSK := m.getClusterSK(targetCluster)

	// Get current values to ensure conditional updates work correctly
	config, err := m.GetGroupConfig(ctx, namespace, name)
	if err != nil {
		return fmt.Errorf("failed to get current config for transaction: %w", err)
	}

	// Create transaction items
	var transactItems []types.TransactWriteItem

	// 1. Update group config to change ownership with condition check
	// For failback: set PreviousOwner to empty since we're returning to the original state
	updateConfigExpr := "SET ownerCluster = :targetCluster, previousOwner = :emptyValue, version = :newVersion, lastUpdated = :timestamp"
	configCondition := "ownerCluster = :sourceCluster AND version = :currentVersion"

	if config.LastFailover == nil {
		updateConfigExpr += ", lastFailover = :failoverRef"
	} else {
		updateConfigExpr += ", lastFailover.#ts = :failoverTimestamp, lastFailover.#nm = :failoverName, lastFailover.#ns = :namespace"
	}

	failoverRefJSON, _ := json.Marshal(FailoverReference{
		Name:      failbackName,
		Namespace: namespace,
		Timestamp: startTime,
	})

	configItem := types.TransactWriteItem{
		Update: &types.Update{
			TableName: &m.tableName,
			Key: map[string]types.AttributeValue{
				"PK": &types.AttributeValueMemberS{Value: pk},
				"SK": &types.AttributeValueMemberS{Value: configSK},
			},
			UpdateExpression:    aws.String(updateConfigExpr),
			ConditionExpression: aws.String(configCondition),
			ExpressionAttributeNames: map[string]string{
				"#ts": "timestamp",
				"#nm": "name",
				"#ns": "namespace",
			},
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":targetCluster":     &types.AttributeValueMemberS{Value: targetCluster},
				":sourceCluster":     &types.AttributeValueMemberS{Value: sourceCluster},
				":emptyValue":        &types.AttributeValueMemberS{Value: ""},
				":currentVersion":    &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", config.Version)},
				":newVersion":        &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", config.Version+1)},
				":timestamp":         &types.AttributeValueMemberS{Value: time.Now().Format(time.RFC3339)},
				":failoverRef":       &types.AttributeValueMemberS{Value: string(failoverRefJSON)},
				":failoverTimestamp": &types.AttributeValueMemberS{Value: startTime.Format(time.RFC3339)},
				":failoverName":      &types.AttributeValueMemberS{Value: failbackName},
				":namespace":         &types.AttributeValueMemberS{Value: namespace},
			},
		},
	}
	transactItems = append(transactItems, configItem)

	// 2. Update source cluster (current owner) status to FAILBACK state
	sourceItem := types.TransactWriteItem{
		Update: &types.Update{
			TableName: &m.tableName,
			Key: map[string]types.AttributeValue{
				"PK": &types.AttributeValueMemberS{Value: pk},
				"SK": &types.AttributeValueMemberS{Value: sourceSK},
			},
			UpdateExpression: aws.String("SET #state = :failbackState, lastHeartbeat = :timestamp"),
			ExpressionAttributeNames: map[string]string{
				"#state": "state",
			},
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":failbackState": &types.AttributeValueMemberS{Value: StateFailback},
				":timestamp":     &types.AttributeValueMemberS{Value: time.Now().Format(time.RFC3339)},
			},
		},
	}
	transactItems = append(transactItems, sourceItem)

	// 3. Update target cluster (previous owner) to PRIMARY
	targetItem := types.TransactWriteItem{
		Update: &types.Update{
			TableName: &m.tableName,
			Key: map[string]types.AttributeValue{
				"PK": &types.AttributeValueMemberS{Value: pk},
				"SK": &types.AttributeValueMemberS{Value: targetSK},
			},
			UpdateExpression: aws.String("SET #state = :primaryState, lastHeartbeat = :timestamp"),
			ExpressionAttributeNames: map[string]string{
				"#state": "state",
			},
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":primaryState": &types.AttributeValueMemberS{Value: StatePrimary},
				":timestamp":    &types.AttributeValueMemberS{Value: time.Now().Format(time.RFC3339)},
			},
		},
	}
	transactItems = append(transactItems, targetItem)

	// Execute the transaction
	_, err = m.client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: transactItems,
	})

	if err != nil {
		// Handle different types of transaction errors
		var txnCanceledException *types.TransactionCanceledException
		if errors.As(err, &txnCanceledException) {
			// Check specific reasons for transaction failure
			reasons := txnCanceledException.CancellationReasons

			// Log detailed error information for each item
			for i, reason := range reasons {
				if reason.Code != nil {
					itemDesc := "unknown"
					if i == 0 {
						itemDesc = "group config"
					} else if i == 1 {
						itemDesc = "source cluster"
					} else if i == 2 {
						itemDesc = "target cluster"
					}

					logger.Error(nil, "Transaction item failed",
						"item", itemDesc,
						"code", *reason.Code,
						"message", aws.ToString(reason.Message))
				}
			}

			return fmt.Errorf("failback transaction cancelled: %w", err)
		}

		return fmt.Errorf("failed to execute failback transaction: %w", err)
	}

	return nil
}

// ValidateFailoverPreconditions validates that preconditions for a failover are met
func (m *OperationsManager) ValidateFailoverPreconditions(ctx context.Context, namespace, name, targetCluster string, skipHealthCheck bool) error {
	logger := log.FromContext(ctx).WithValues(
		"namespace", namespace,
		"name", name,
		"targetCluster", targetCluster,
		"skipHealthCheck", skipHealthCheck,
	)
	logger.V(1).Info("Validating failover preconditions")

	// 1. Check if a lock already exists
	locked, lockedBy, err := m.IsLocked(ctx, namespace, name)
	if err != nil {
		return fmt.Errorf("failed to check lock status: %w", err)
	}
	if locked {
		return fmt.Errorf("failover group is locked by: %s", lockedBy)
	}

	// 2. Get the group configuration
	config, err := m.GetGroupConfig(ctx, namespace, name)
	if err != nil {
		return fmt.Errorf("failed to check config: %w", err)
	}

	// 3. Check if the group is suspended
	if config.Suspended {
		return fmt.Errorf("failover group is suspended: %s", config.SuspensionReason)
	}

	// 4. Check target cluster health
	if !skipHealthCheck {
		status, err := m.GetClusterStatus(ctx, namespace, name, targetCluster)
		if err != nil {
			return fmt.Errorf("failed to check target cluster health: %w", err)
		}
		if status.Health != HealthOK {
			return fmt.Errorf("target cluster %s is not healthy: %s", targetCluster, status.Health)
		}
	}

	return nil
}

// UpdateSuspension updates the suspension status of a FailoverGroup
func (m *OperationsManager) UpdateSuspension(ctx context.Context, namespace, name string, suspended bool, reason string) error {
	logger := log.FromContext(ctx).WithValues(
		"namespace", namespace,
		"name", name,
		"suspended", suspended,
	)
	logger.V(1).Info("Updating suspension status")

	// Get current config
	config, err := m.GetGroupConfig(ctx, namespace, name)
	if err != nil {
		return fmt.Errorf("failed to get current config: %w", err)
	}

	// Update the suspension fields
	config.Suspended = suspended
	config.SuspensionReason = reason
	config.Version++
	config.LastUpdated = time.Now()

	// Save the updated config
	return m.UpdateGroupConfig(ctx, config)
}

// AcquireLock attempts to acquire a lock for a FailoverGroup
func (m *OperationsManager) AcquireLock(ctx context.Context, namespace, name, reason string) (string, error) {
	logger := log.FromContext(ctx).WithValues(
		"namespace", namespace,
		"name", name,
		"reason", reason,
	)
	logger.V(1).Info("Acquiring lock")

	// Generate a unique lease token
	leaseToken := uuid.New().String()

	// Check if the lock is already held
	locked, lockedBy, err := m.IsLocked(ctx, namespace, name)
	if err != nil {
		return "", fmt.Errorf("failed to check existing lock: %w", err)
	}

	if locked {
		return "", fmt.Errorf("lock already held by %s", lockedBy)
	}

	// Set lock expiration time (10 minutes is a reasonable default)
	now := time.Now()
	expiresAt := now.Add(10 * time.Minute)

	// Create a lock record
	lockItem := map[string]types.AttributeValue{
		"PK":             &types.AttributeValueMemberS{Value: m.getGroupPK(namespace, name)},
		"SK":             &types.AttributeValueMemberS{Value: "LOCK"},
		"operatorID":     &types.AttributeValueMemberS{Value: m.operatorID},
		"groupNamespace": &types.AttributeValueMemberS{Value: namespace},
		"groupName":      &types.AttributeValueMemberS{Value: name},
		"lockedBy":       &types.AttributeValueMemberS{Value: m.clusterName},
		"lockReason":     &types.AttributeValueMemberS{Value: reason},
		"acquiredAt":     &types.AttributeValueMemberS{Value: now.Format(time.RFC3339)},
		"expiresAt":      &types.AttributeValueMemberS{Value: expiresAt.Format(time.RFC3339)},
		"leaseToken":     &types.AttributeValueMemberS{Value: leaseToken},
	}

	// Create a conditional write to ensure the lock doesn't already exist
	// or if it exists, that it has expired
	input := &dynamodb.PutItemInput{
		TableName:           &m.tableName,
		Item:                lockItem,
		ConditionExpression: aws.String("attribute_not_exists(#PK) OR (attribute_exists(#PK) AND #expiresAt < :now)"),
		ExpressionAttributeNames: map[string]string{
			"#PK":        "PK",
			"#expiresAt": "expiresAt",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":now": &types.AttributeValueMemberS{Value: now.Format(time.RFC3339)},
		},
	}

	// Attempt to write the lock record
	_, err = m.client.PutItem(ctx, input)
	if err != nil {
		var conditionErr *types.ConditionalCheckFailedException
		if errors.As(err, &conditionErr) {
			// This means the lock already exists and hasn't expired
			return "", fmt.Errorf("lock already held by another process")
		}
		return "", fmt.Errorf("failed to acquire lock: %w", err)
	}

	return leaseToken, nil
}

// ReleaseLock releases a lock for a FailoverGroup
func (m *OperationsManager) ReleaseLock(ctx context.Context, namespace, name, leaseToken string) error {
	logger := log.FromContext(ctx).WithValues(
		"namespace", namespace,
		"name", name,
	)
	logger.V(1).Info("Releasing lock")

	// Create the key for the lock record
	key := map[string]types.AttributeValue{
		"PK": &types.AttributeValueMemberS{Value: m.getGroupPK(namespace, name)},
		"SK": &types.AttributeValueMemberS{Value: "LOCK"},
	}

	// Set up a conditional delete to ensure we only delete the lock if it's ours
	input := &dynamodb.DeleteItemInput{
		TableName:           &m.tableName,
		Key:                 key,
		ConditionExpression: aws.String("attribute_exists(#PK) AND #leaseToken = :leaseToken"),
		ExpressionAttributeNames: map[string]string{
			"#PK":         "PK",
			"#leaseToken": "leaseToken",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":leaseToken": &types.AttributeValueMemberS{Value: leaseToken},
		},
	}

	// Execute the delete
	_, err := m.client.DeleteItem(ctx, input)
	if err != nil {
		var conditionErr *types.ConditionalCheckFailedException
		if errors.As(err, &conditionErr) {
			// This means the lock doesn't exist or the lease token doesn't match
			return fmt.Errorf("lock doesn't exist or lease token doesn't match")
		}
		return fmt.Errorf("failed to release lock: %w", err)
	}

	return nil
}

// IsLocked checks if a FailoverGroup is locked
func (m *OperationsManager) IsLocked(ctx context.Context, namespace, name string) (bool, string, error) {
	logger := log.FromContext(ctx).WithValues(
		"namespace", namespace,
		"name", name,
	)
	logger.V(1).Info("Checking lock status")

	// Create the key for the lock record
	key := map[string]types.AttributeValue{
		"PK": &types.AttributeValueMemberS{Value: m.getGroupPK(namespace, name)},
		"SK": &types.AttributeValueMemberS{Value: "LOCK"},
	}

	// Get the lock record if it exists
	input := &dynamodb.GetItemInput{
		TableName: &m.tableName,
		Key:       key,
	}

	// Execute the query
	result, err := m.client.GetItem(ctx, input)
	if err != nil {
		return false, "", fmt.Errorf("failed to check lock status: %w", err)
	}

	// Check if the item was found
	if len(result.Item) == 0 {
		// No lock exists
		return false, "", nil
	}

	// Check if the lock has expired
	var expiresAt time.Time
	if v, ok := result.Item["expiresAt"]; ok {
		if sv, ok := v.(*types.AttributeValueMemberS); ok {
			var err error
			expiresAt, err = time.Parse(time.RFC3339, sv.Value)
			if err != nil {
				return false, "", fmt.Errorf("invalid expiration time in lock: %w", err)
			}
		}
	}

	if time.Now().After(expiresAt) {
		// Lock has expired
		return false, "", nil
	}

	// Extract who holds the lock
	var lockedBy string
	if v, ok := result.Item["lockedBy"]; ok {
		if sv, ok := v.(*types.AttributeValueMemberS); ok {
			lockedBy = sv.Value
		}
	}

	// Lock exists and hasn't expired
	return true, lockedBy, nil
}

// transferOwnership transfers ownership of a FailoverGroup to a new cluster
func (m *OperationsManager) transferOwnership(ctx context.Context, namespace, name, newOwner string) error {
	logger := log.FromContext(ctx).WithValues(
		"namespace", namespace,
		"name", name,
		"newOwner", newOwner,
	)
	logger.V(1).Info("Transferring ownership")

	// Get the most up-to-date config from DynamoDB
	config, err := m.GetGroupConfig(ctx, namespace, name)
	if err != nil {
		return fmt.Errorf("failed to get current config: %w", err)
	}

	// If the current owner is already the newOwner, we're done
	if config.OwnerCluster == newOwner {
		logger.Info("Target cluster is already the owner, no update needed",
			"currentOwner", config.OwnerCluster)
		return nil
	}

	// Make a copy of the previous owner
	previousOwner := config.OwnerCluster

	// Update the config with the new values
	config.PreviousOwner = previousOwner
	config.OwnerCluster = newOwner
	config.Version++ // Increment the version
	config.LastUpdated = time.Now()

	// Create or update the LastFailover reference
	if config.LastFailover == nil {
		config.LastFailover = &FailoverReference{
			Name:      fmt.Sprintf("forced-failover-%s", time.Now().Format("20060102-150405")),
			Namespace: namespace,
			Timestamp: time.Now(),
		}
	} else {
		config.LastFailover.Timestamp = time.Now()
	}

	// Try to update the config using the standard method first
	err = m.UpdateGroupConfig(ctx, config)
	if err != nil {
		// Check if we have a version conflict
		if strings.Contains(err.Error(), "version conflict") ||
			strings.Contains(err.Error(), "conditional check failed") {
			logger.Info("Version conflict detected, retrying with forced update")

			// For a forced update, we need to bypass version checking
			// Create a direct update with just the ownership change
			pk := m.getGroupPK(namespace, name)
			configSK := "CONFIG"

			updateInput := &dynamodb.UpdateItemInput{
				TableName: &m.tableName,
				Key: map[string]types.AttributeValue{
					"PK": &types.AttributeValueMemberS{Value: pk},
					"SK": &types.AttributeValueMemberS{Value: configSK},
				},
				UpdateExpression: aws.String("SET ownerCluster = :newOwner, previousOwner = :prevOwner, version = :newVersion, lastUpdated = :now"),
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":newOwner":   &types.AttributeValueMemberS{Value: newOwner},
					":prevOwner":  &types.AttributeValueMemberS{Value: previousOwner},
					":newVersion": &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", config.Version+1)},
					":now":        &types.AttributeValueMemberS{Value: time.Now().Format(time.RFC3339)},
				},
				// No condition expression - this will force the update regardless of version
			}

			_, err = m.client.UpdateItem(ctx, updateInput)
			if err != nil {
				return fmt.Errorf("forced ownership update failed: %w", err)
			}

			logger.Info("Successfully forced ownership update",
				"previousOwner", previousOwner,
				"newOwner", newOwner)
			return nil
		}

		// For other types of errors, return as usual
		return fmt.Errorf("failed to update ownership: %w", err)
	}

	logger.Info("Successfully transferred ownership",
		"previousOwner", previousOwner,
		"newOwner", newOwner)
	return nil
}

// recordFailoverEvent records a failover event in the history
func (m *OperationsManager) recordFailoverEvent(ctx context.Context, namespace, name, failoverName, sourceCluster, targetCluster, reason string, startTime, endTime time.Time, status string, downtime, duration int64) error {
	logger := log.FromContext(ctx).WithValues(
		"namespace", namespace,
		"name", name,
		"failoverName", failoverName,
	)
	logger.V(1).Info("Recording failover event")

	// Create the attributes for the history record
	historyItem := map[string]types.AttributeValue{
		"PK":             &types.AttributeValueMemberS{Value: m.getGroupPK(namespace, name)},
		"SK":             &types.AttributeValueMemberS{Value: m.getHistorySK(startTime)},
		"operatorID":     &types.AttributeValueMemberS{Value: m.operatorID},
		"groupNamespace": &types.AttributeValueMemberS{Value: namespace},
		"groupName":      &types.AttributeValueMemberS{Value: name},
		"failoverName":   &types.AttributeValueMemberS{Value: failoverName},
		"sourceCluster":  &types.AttributeValueMemberS{Value: sourceCluster},
		"targetCluster":  &types.AttributeValueMemberS{Value: targetCluster},
		"startTime":      &types.AttributeValueMemberS{Value: startTime.Format(time.RFC3339)},
		"endTime":        &types.AttributeValueMemberS{Value: endTime.Format(time.RFC3339)},
		"status":         &types.AttributeValueMemberS{Value: status},
		"downtime":       &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", downtime)},
		"duration":       &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", duration)},
	}

	// Add reason if provided
	if reason != "" {
		historyItem["reason"] = &types.AttributeValueMemberS{Value: reason}
	}

	// Create the PutItem input
	input := &dynamodb.PutItemInput{
		TableName: &m.tableName,
		Item:      historyItem,
	}

	// Execute the put operation
	_, err := m.client.PutItem(ctx, input)
	if err != nil {
		return fmt.Errorf("failed to record failover event: %w", err)
	}

	// Also update the LastFailover reference in the group configuration
	config, err := m.GetGroupConfig(ctx, namespace, name)
	if err != nil {
		logger.Error(err, "Failed to get group config for updating LastFailover reference")
		// Continue despite this error, as the history record was successfully created
		return nil
	}

	// Update the LastFailover reference
	config.LastFailover = &FailoverReference{
		Name:      failoverName,
		Namespace: namespace,
		Timestamp: startTime,
	}

	// Save the updated config
	if err := m.UpdateGroupConfig(ctx, config); err != nil {
		logger.Error(err, "Failed to update LastFailover reference")
		// Continue despite this error
	}

	return nil
}

// DetectAndReportProblems checks for problems in a FailoverGroup and returns a list of issues
func (m *OperationsManager) DetectAndReportProblems(ctx context.Context, namespace, name string) ([]string, error) {
	logger := log.FromContext(ctx).WithValues(
		"namespace", namespace,
		"name", name,
	)
	logger.V(1).Info("Detecting problems")

	// TODO: Implement actual problem detection
	// This is a placeholder implementation
	return []string{}, nil
}

// updateClusterStateForFailover updates a cluster's state in the cluster status record during failover operations
func (m *OperationsManager) updateClusterStateForFailover(ctx context.Context, namespace, name, clusterName, state string) error {
	logger := log.FromContext(ctx).WithValues(
		"namespace", namespace,
		"name", name,
		"cluster", clusterName,
		"state", state,
	)
	logger.V(1).Info("Updating cluster state for failover")

	// Get the current status
	status, err := m.GetClusterStatus(ctx, namespace, name, clusterName)
	if err != nil {
		logger.Error(err, "Failed to get cluster status")
		// Create a minimal status if it does not exist
		status = &ClusterStatusRecord{
			GroupNamespace: namespace,
			GroupName:      name,
			ClusterName:    clusterName,
			Health:         HealthOK,
			State:          state,
			Components:     "{}",
		}
	} else {
		// Update the state
		status.State = state
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

	// Update the status
	return m.UpdateClusterStatus(ctx, namespace, name, clusterName, status.Health, state, status.Components)
}

// RemoveClusterStatus removes a cluster's status entry from DynamoDB
func (m *OperationsManager) RemoveClusterStatus(ctx context.Context, namespace, name, clusterName string) error {
	logger := log.FromContext(ctx).WithValues(
		"namespace", namespace,
		"name", name,
		"clusterName", clusterName,
	)
	logger.Info("Removing cluster status from DynamoDB")

	// Calculate the key for the status item using the same methods as other operations
	pk := m.getGroupPK(namespace, name)
	sk := m.getClusterSK(clusterName)

	// Delete the item
	_, err := m.client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: &m.tableName,
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: pk},
			"SK": &types.AttributeValueMemberS{Value: sk},
		},
	})

	if err != nil {
		return fmt.Errorf("failed to delete cluster status: %w", err)
	}

	return nil
}

// TransferOwnership transfers ownership of a FailoverGroup to a new cluster
func (m *OperationsManager) TransferOwnership(ctx context.Context, namespace, name, newOwner string) error {
	return m.transferOwnership(ctx, namespace, name, newOwner)
}
