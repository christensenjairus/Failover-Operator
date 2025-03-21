package dynamodb

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewOperationsManager(t *testing.T) {
	// Setup
	baseManager := &BaseManager{
		client:      &EnhancedTestDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}

	// Call the function under test
	operationsManager := NewOperationsManager(baseManager)

	// Verify the results
	assert.NotNil(t, operationsManager, "OperationsManager should not be nil")
	assert.Equal(t, baseManager, operationsManager.BaseManager, "BaseManager should be set correctly")
	assert.NotNil(t, operationsManager.stateManager, "StateManager should be set")
}

func TestExecuteFailover(t *testing.T) {
	// Setup
	baseManager := &BaseManager{
		client:      &EnhancedTestDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	operationsManager := NewOperationsManager(baseManager)
	ctx := context.Background()
	namespace := "test-namespace"
	name := "test-name"
	failoverName := "test-failover"
	targetCluster := "target-cluster" // Different than source cluster
	reason := "Planned failover for testing"
	forceFastMode := false

	// Call the function under test
	err := operationsManager.ExecuteFailover(ctx, namespace, name, failoverName, targetCluster, reason, forceFastMode)

	// Verify the results
	assert.NoError(t, err, "ExecuteFailover should not return an error")
}

func TestExecuteFailback(t *testing.T) {
	// Create a client with the GetGroupConfigFn predefined
	client := &EnhancedTestDynamoDBClient{}

	// Create the operations manager with the base setup
	baseManager := NewBaseManager(client, "test-table", "test-cluster", "test-operator")
	opManager := NewOperationsManager(baseManager)

	// Test execution - this will fail with "no previous owner" which is expected
	// since we're testing with a default empty config
	err := opManager.ExecuteFailback(context.Background(), "test-namespace", "test-name", "Test failback")

	// We should get an error stating there's no previous owner
	assert.Error(t, err, "ExecuteFailback should return an error when there is no previous owner")
	assert.Contains(t, err.Error(), "no previous owner to failback to", "Error message should indicate missing previous owner")
}

func TestValidateFailoverPreconditions(t *testing.T) {
	// Setup
	baseManager := &BaseManager{
		client:      &EnhancedTestDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	operationsManager := NewOperationsManager(baseManager)
	ctx := context.Background()
	namespace := "test-namespace"
	name := "test-name"
	targetCluster := "target-cluster"
	skipHealthCheck := false

	// Call the function under test
	err := operationsManager.ValidateFailoverPreconditions(ctx, namespace, name, targetCluster, skipHealthCheck)

	// Verify the results
	assert.NoError(t, err, "ValidateFailoverPreconditions should not return an error")
}

func TestUpdateSuspension(t *testing.T) {
	// Setup
	baseManager := &BaseManager{
		client:      &EnhancedTestDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	operationsManager := NewOperationsManager(baseManager)
	ctx := context.Background()
	namespace := "test-namespace"
	name := "test-name"
	suspended := true
	reason := "Maintenance window"

	// Call the function under test
	err := operationsManager.UpdateSuspension(ctx, namespace, name, suspended, reason)

	// Verify the results
	assert.NoError(t, err, "UpdateSuspension should not return an error")
}

func TestAcquireLock(t *testing.T) {
	// Setup
	baseManager := &BaseManager{
		client:      &EnhancedTestDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	operationsManager := NewOperationsManager(baseManager)
	ctx := context.Background()
	namespace := "test-namespace"
	name := "test-name"
	reason := "Testing lock acquisition"

	// Call the function under test
	leaseToken, err := operationsManager.AcquireLock(ctx, namespace, name, reason)

	// Verify the results
	assert.NoError(t, err, "AcquireLock should not return an error")
	assert.NotEmpty(t, leaseToken, "Lease token should not be empty")
}

func TestReleaseLock(t *testing.T) {
	// Setup
	baseManager := &BaseManager{
		client:      &EnhancedTestDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	operationsManager := NewOperationsManager(baseManager)
	ctx := context.Background()
	namespace := "test-namespace"
	name := "test-name"
	leaseToken := "test-lease-token"

	// Call the function under test
	err := operationsManager.ReleaseLock(ctx, namespace, name, leaseToken)

	// Verify the results
	assert.NoError(t, err, "ReleaseLock should not return an error")
}

func TestIsLocked(t *testing.T) {
	// Setup
	baseManager := &BaseManager{
		client:      &EnhancedTestDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	operationsManager := NewOperationsManager(baseManager)
	ctx := context.Background()
	namespace := "test-namespace"
	name := "test-name"

	// Call the function under test
	locked, lockedBy, err := operationsManager.IsLocked(ctx, namespace, name)

	// Verify the results
	assert.NoError(t, err, "IsLocked should not return an error")
	assert.False(t, locked, "Lock should not be acquired in the test")
	assert.Empty(t, lockedBy, "LockedBy should be empty when not locked")
}

func TestTransferOwnership(t *testing.T) {
	// Setup
	baseManager := &BaseManager{
		client:      &EnhancedTestDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	operationsManager := NewOperationsManager(baseManager)
	ctx := context.Background()
	namespace := "test-namespace"
	name := "test-name"
	newOwner := "new-owner-cluster"

	// Call the function under test
	err := operationsManager.transferOwnership(ctx, namespace, name, newOwner)

	// Verify the results
	assert.NoError(t, err, "transferOwnership should not return an error")
}

func TestRecordFailoverEvent(t *testing.T) {
	// Setup
	baseManager := &BaseManager{
		client:      &EnhancedTestDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	operationsManager := NewOperationsManager(baseManager)
	ctx := context.Background()
	namespace := "test-namespace"
	name := "test-name"
	failoverName := "test-failover"
	sourceCluster := "source-cluster"
	targetCluster := "target-cluster"
	reason := "Planned failover for testing"
	startTime := time.Now().Add(-5 * time.Minute)
	endTime := time.Now()
	status := "SUCCESS"
	downtime := int64(30)
	duration := int64(300)

	// Call the function under test
	err := operationsManager.recordFailoverEvent(ctx, namespace, name, failoverName, sourceCluster, targetCluster, reason, startTime, endTime, status, downtime, duration)

	// Verify the results
	assert.NoError(t, err, "recordFailoverEvent should not return an error")
}

func TestDetectAndReportProblems(t *testing.T) {
	// Setup
	mockClient := &EnhancedTestDynamoDBClient{
		ProblemsReturnFn: func() []string {
			return []string{"Test problem detected"}
		},
	}

	baseManager := &BaseManager{
		client:      mockClient,
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	operationsManager := NewOperationsManager(baseManager)
	ctx := context.Background()
	namespace := "test-namespace"
	name := "test-name"

	// Call the function under test
	problems, err := operationsManager.DetectAndReportProblems(ctx, namespace, name)

	// Verify the results
	assert.NoError(t, err, "DetectAndReportProblems should not return an error")
	assert.NotNil(t, problems, "Problems should not be nil even if empty")
	assert.Contains(t, problems, "Test problem detected", "Problems should contain the test problem")
}
