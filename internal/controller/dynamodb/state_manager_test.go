package dynamodb

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewStateManager(t *testing.T) {
	// Setup
	baseManager := &Manager{
		client:      &mockDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}

	// Call the function under test
	stateManager := NewStateManager(baseManager)

	// Verify the results
	assert.NotNil(t, stateManager, "StateManager should not be nil")
	assert.Equal(t, baseManager, stateManager.Manager, "Base manager should be set correctly")
}

func TestGetState(t *testing.T) {
	// Setup
	baseManager := &Manager{
		client:      &mockDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	stateManager := NewStateManager(baseManager)
	ctx := context.Background()
	groupID := "test-group"

	// Call the function under test
	state, err := stateManager.GetState(ctx, groupID)

	// Verify the results
	assert.NoError(t, err, "GetState should not return an error")
	assert.NotNil(t, state, "State should not be nil")
	assert.Equal(t, groupID, state.GroupID, "GroupID should match")
}

func TestUpdateState(t *testing.T) {
	// Setup
	baseManager := &Manager{
		client:      &mockDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	stateManager := NewStateManager(baseManager)
	ctx := context.Background()
	state := &GroupState{
		GroupID:     "test-group",
		Status:      "Available",
		CurrentRole: "Primary",
		LastUpdate:  1234567890,
	}

	// Call the function under test
	err := stateManager.UpdateState(ctx, state)

	// Verify the results
	assert.NoError(t, err, "UpdateState should not return an error")
}

func TestResetFailoverCounter(t *testing.T) {
	// Setup
	baseManager := &Manager{
		client:      &mockDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	stateManager := NewStateManager(baseManager)
	ctx := context.Background()
	groupID := "test-group"

	// Call the function under test
	err := stateManager.ResetFailoverCounter(ctx, groupID)

	// Verify the results
	assert.NoError(t, err, "ResetFailoverCounter should not return an error")
}

func TestIncrementFailoverCounter(t *testing.T) {
	// Setup
	baseManager := &Manager{
		client:      &mockDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	stateManager := NewStateManager(baseManager)
	ctx := context.Background()
	groupID := "test-group"

	// Call the function under test
	newCount, err := stateManager.IncrementFailoverCounter(ctx, groupID)

	// Verify the results
	assert.NoError(t, err, "IncrementFailoverCounter should not return an error")
	assert.Equal(t, 1, newCount, "New count should be 1")
}
