package dynamodb

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewStateManager(t *testing.T) {
	// Setup
	baseManager := &BaseManager{
		client:      &EnhancedTestDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}

	// Call the function under test
	stateManager := NewStateManager(baseManager)

	// Verify the results
	assert.NotNil(t, stateManager, "StateManager should not be nil")
	assert.Equal(t, baseManager, stateManager.BaseManager, "Base manager should be set correctly")
}

func TestGetGroupState(t *testing.T) {
	// Setup
	baseManager := &BaseManager{
		client:      &EnhancedTestDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	stateManager := NewStateManager(baseManager)
	ctx := context.Background()
	namespace := "test-namespace"
	name := "test-name"

	// Call the function under test
	state, err := stateManager.GetGroupState(ctx, namespace, name)

	// Verify the results
	assert.NoError(t, err, "GetGroupState should not return an error")
	assert.NotNil(t, state, "State should not be nil")
	assert.Equal(t, namespace+"/"+name, state.GroupID, "GroupID should match")
}

func TestGetGroupConfig(t *testing.T) {
	// Setup
	baseManager := &BaseManager{
		client:      &EnhancedTestDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	stateManager := NewStateManager(baseManager)
	ctx := context.Background()
	namespace := "test-namespace"
	name := "test-name"

	// Call the function under test
	config, err := stateManager.GetGroupConfig(ctx, namespace, name)

	// Verify the results
	assert.NoError(t, err, "GetGroupConfig should not return an error")
	assert.NotNil(t, config, "Config should not be nil")
	assert.Equal(t, namespace, config.GroupNamespace, "Namespace should match")
	assert.Equal(t, name, config.GroupName, "Name should match")
	assert.Equal(t, baseManager.operatorID, config.OperatorID, "OperatorID should match")

	// Check GSI fields are set correctly
	assert.Equal(t, baseManager.getOperatorGSI1PK(), config.GSI1PK, "GSI1PK should match")
	assert.Equal(t, baseManager.getGroupGSI1SK(namespace, name), config.GSI1SK, "GSI1SK should match")
}

func TestUpdateGroupConfig(t *testing.T) {
	// Setup
	baseManager := &BaseManager{
		client:      &EnhancedTestDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	stateManager := NewStateManager(baseManager)
	ctx := context.Background()
	config := &GroupConfigRecord{
		PK:             baseManager.getGroupPK("test-namespace", "test-name"),
		SK:             "CONFIG",
		GSI1PK:         baseManager.getOperatorGSI1PK(),
		GSI1SK:         baseManager.getGroupGSI1SK("test-namespace", "test-name"),
		OperatorID:     baseManager.operatorID,
		GroupNamespace: "test-namespace",
		GroupName:      "test-name",
		OwnerCluster:   baseManager.clusterName,
		Version:        1,
		LastUpdated:    time.Now(),
	}

	// Call the function under test
	err := stateManager.UpdateGroupConfig(ctx, config)

	// Verify the results
	assert.NoError(t, err, "UpdateGroupConfig should not return an error")
}

func TestGetClusterStatus(t *testing.T) {
	// Setup
	baseManager := &BaseManager{
		client:      &EnhancedTestDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	stateManager := NewStateManager(baseManager)
	ctx := context.Background()
	namespace := "test-namespace"
	name := "test-name"
	clusterName := "test-cluster"

	// Call the function under test
	status, err := stateManager.GetClusterStatus(ctx, namespace, name, clusterName)

	// Verify the results
	assert.NoError(t, err, "GetClusterStatus should not return an error")
	assert.NotNil(t, status, "Status should not be nil")
	assert.Equal(t, namespace, status.GroupNamespace, "Namespace should match")
	assert.Equal(t, name, status.GroupName, "Name should match")
	assert.Equal(t, clusterName, status.ClusterName, "ClusterName should match")
	assert.Equal(t, baseManager.operatorID, status.OperatorID, "OperatorID should match")

	// Check GSI fields are set correctly
	assert.Equal(t, baseManager.getClusterGSI1PK(clusterName), status.GSI1PK, "GSI1PK should match")
	assert.Equal(t, baseManager.getGroupGSI1SK(namespace, name), status.GSI1SK, "GSI1SK should match")
}

func TestUpdateClusterStatus(t *testing.T) {
	// Mock client
	client := &MockDynamoDBClient{}
	baseManager := NewBaseManager(client, "test-table", "test-cluster", "test-operator")
	stateManager := NewStateManager(baseManager)

	// Test context
	ctx := context.Background()

	// Test parameters
	namespace := "test-namespace"
	name := "test-group"
	health := "OK"
	state := "PRIMARY"

	// Create StatusData for testing
	statusData := &StatusData{
		Workloads: []ResourceStatus{
			{
				Kind:   "Deployment",
				Name:   "web-app",
				Health: "OK",
				Status: "Running normally",
			},
		},
	}

	// Test the function
	err := stateManager.UpdateClusterStatus(ctx, namespace, name, health, state, statusData)

	// Check result
	assert.NoError(t, err, "UpdateClusterStatus should not return an error")
}

// For backward compatibility testing
func TestUpdateClusterStatusLegacy(t *testing.T) {
	// Mock client
	client := &MockDynamoDBClient{}
	baseManager := NewBaseManager(client, "test-table", "test-cluster", "test-operator")
	stateManager := NewStateManager(baseManager)

	// Test context
	ctx := context.Background()

	// Test parameters
	namespace := "test-namespace"
	name := "test-group"
	health := "OK"
	state := "PRIMARY"

	// Create legacy components map for testing
	components := map[string]ComponentStatus{
		"web-app": {
			Health:  "OK",
			Message: "Web app is healthy",
		},
		"database": {
			Health:  "OK",
			Message: "Database is healthy",
		},
	}

	// Test the function
	err := stateManager.UpdateClusterStatusLegacy(ctx, namespace, name, health, state, components)

	// Check result
	assert.NoError(t, err, "UpdateClusterStatusLegacy should not return an error")
}

func TestGetAllClusterStatuses(t *testing.T) {
	// Setup
	baseManager := &BaseManager{
		client:      &EnhancedTestDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	stateManager := NewStateManager(baseManager)
	ctx := context.Background()
	namespace := "test-namespace"
	name := "test-name"

	// Call the function under test
	statuses, err := stateManager.GetAllClusterStatuses(ctx, namespace, name)

	// Verify the results
	assert.NoError(t, err, "GetAllClusterStatuses should not return an error")
	assert.NotNil(t, statuses, "Statuses should not be nil")
	assert.NotEmpty(t, statuses, "Statuses should not be empty")
	assert.Contains(t, statuses, baseManager.clusterName, "Statuses should contain the current cluster")
}

func TestGetFailoverHistory(t *testing.T) {
	// Setup
	baseManager := &BaseManager{
		client:      &EnhancedTestDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	stateManager := NewStateManager(baseManager)
	ctx := context.Background()
	namespace := "test-namespace"
	name := "test-name"
	limit := 1

	// Call the function under test
	history, err := stateManager.GetFailoverHistory(ctx, namespace, name, limit)

	// Verify the results
	assert.NoError(t, err, "GetFailoverHistory should not return an error")
	assert.NotNil(t, history, "History should not be nil")
	assert.Len(t, history, 1, "History should have exactly one element with limit=1")
}

func TestSyncClusterState(t *testing.T) {
	// Setup
	baseManager := &BaseManager{
		client:      &EnhancedTestDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	stateManager := NewStateManager(baseManager)
	ctx := context.Background()
	namespace := "test-namespace"
	name := "test-name"

	// Call the function under test
	err := stateManager.SyncClusterState(ctx, namespace, name)

	// Verify the results
	assert.NoError(t, err, "SyncClusterState should not return an error")
}

func TestDetectStaleHeartbeats(t *testing.T) {
	// Setup
	mockClient := &EnhancedTestDynamoDBClient{
		StaleClustersReturnFn: func() []string {
			return []string{"stale-cluster-1"}
		},
	}

	baseManager := &BaseManager{
		client:      mockClient,
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	stateManager := NewStateManager(baseManager)
	ctx := context.Background()
	namespace := "test-namespace"
	name := "test-name"

	// Call the function under test
	staleClusters, err := stateManager.DetectStaleHeartbeats(ctx, namespace, name)

	// Verify the results
	assert.NoError(t, err, "DetectStaleHeartbeats should not return an error")
	assert.NotNil(t, staleClusters, "StaleClusters should not be nil even if empty")
	assert.Contains(t, staleClusters, "stale-cluster-1", "Should include the mocked stale cluster")
}
