package dynamodb

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewHeartbeatManager(t *testing.T) {
	// Setup
	baseManager := &Manager{
		client:      &mockDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}

	// Call the function under test
	heartbeatManager := NewHeartbeatManager(baseManager)

	// Verify the results
	assert.NotNil(t, heartbeatManager, "HeartbeatManager should not be nil")
	assert.Equal(t, baseManager, heartbeatManager.Manager, "Base manager should be set correctly")
}

func TestUpdateHeartbeat(t *testing.T) {
	// Setup
	baseManager := &Manager{
		client:      &mockDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	heartbeatManager := NewHeartbeatManager(baseManager)
	ctx := context.Background()

	// Call the function under test
	err := heartbeatManager.UpdateHeartbeat(ctx)

	// Verify the results
	assert.NoError(t, err, "UpdateHeartbeat should not return an error")
}

func TestGetHeartbeats(t *testing.T) {
	// Setup
	baseManager := &Manager{
		client:      &mockDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	heartbeatManager := NewHeartbeatManager(baseManager)
	ctx := context.Background()

	// Call the function under test
	heartbeats, err := heartbeatManager.GetHeartbeats(ctx)

	// Verify the results
	assert.NoError(t, err, "GetHeartbeats should not return an error")
	assert.NotNil(t, heartbeats, "Heartbeats should not be nil")
	assert.Len(t, heartbeats, 1, "There should be one heartbeat")
}

func TestCheckClusterHealth(t *testing.T) {
	// Setup
	baseManager := &Manager{
		client:      &mockDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	heartbeatManager := NewHeartbeatManager(baseManager)
	ctx := context.Background()
	clusterName := "test-cluster"

	// Call the function under test
	healthy, err := heartbeatManager.CheckClusterHealth(ctx, clusterName)

	// Verify the results
	assert.NoError(t, err, "CheckClusterHealth should not return an error")
	assert.True(t, healthy, "Cluster should be healthy")
}

func TestIsOperatorActive(t *testing.T) {
	// Setup
	baseManager := &Manager{
		client:      &mockDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	heartbeatManager := NewHeartbeatManager(baseManager)
	ctx := context.Background()
	clusterName := "test-cluster"
	operatorID := "test-operator"

	// Call the function under test
	active, err := heartbeatManager.IsOperatorActive(ctx, clusterName, operatorID)

	// Verify the results
	assert.NoError(t, err, "IsOperatorActive should not return an error")
	assert.True(t, active, "Operator should be active")
}

func TestGetActiveOperators(t *testing.T) {
	// Setup
	baseManager := &Manager{
		client:      &mockDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	heartbeatManager := NewHeartbeatManager(baseManager)
	ctx := context.Background()

	// Call the function under test
	operators, err := heartbeatManager.GetActiveOperators(ctx)

	// Verify the results
	assert.NoError(t, err, "GetActiveOperators should not return an error")
	assert.NotNil(t, operators, "Operators should not be nil")
	assert.Contains(t, operators, "test-cluster", "Operators should contain test-cluster")
	assert.Contains(t, operators["test-cluster"], "test-operator", "Operators for test-cluster should contain test-operator")
}
