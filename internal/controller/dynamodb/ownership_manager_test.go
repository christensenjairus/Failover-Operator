package dynamodb

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewOwnershipManager(t *testing.T) {
	// Setup
	baseManager := &Manager{
		client:      &mockDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}

	// Call the function under test
	ownershipManager := NewOwnershipManager(baseManager)

	// Verify the results
	assert.NotNil(t, ownershipManager, "OwnershipManager should not be nil")
	assert.Equal(t, baseManager, ownershipManager.Manager, "Base manager should be set correctly")
}

func TestGetOwnership(t *testing.T) {
	// Setup
	baseManager := &Manager{
		client:      &mockDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	ownershipManager := NewOwnershipManager(baseManager)
	ctx := context.Background()
	groupID := "test-group"

	// Call the function under test
	ownership, err := ownershipManager.GetOwnership(ctx, groupID)

	// Verify the results
	assert.NoError(t, err, "GetOwnership should not return an error")
	assert.NotNil(t, ownership, "Ownership should not be nil")
	assert.Equal(t, groupID, ownership.GroupID, "GroupID should match")
}

func TestUpdateOwnership(t *testing.T) {
	// Setup
	baseManager := &Manager{
		client:      &mockDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	ownershipManager := NewOwnershipManager(baseManager)
	ctx := context.Background()
	groupID := "test-group"
	newOwner := "new-cluster"
	previousOwner := "old-cluster"

	// Call the function under test
	err := ownershipManager.UpdateOwnership(ctx, groupID, newOwner, previousOwner)

	// Verify the results
	assert.NoError(t, err, "UpdateOwnership should not return an error")
}

func TestGetGroupsOwnedByCluster(t *testing.T) {
	// Setup
	baseManager := &Manager{
		client:      &mockDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	ownershipManager := NewOwnershipManager(baseManager)
	ctx := context.Background()
	clusterName := "test-cluster"

	// Call the function under test
	groups, err := ownershipManager.GetGroupsOwnedByCluster(ctx, clusterName)

	// Verify the results
	assert.NoError(t, err, "GetGroupsOwnedByCluster should not return an error")
	assert.NotNil(t, groups, "Groups should not be nil")
	assert.Len(t, groups, 1, "There should be one group")
	assert.Equal(t, "test-group", groups[0], "The group ID should match")
}

func TestTakeoverInactiveGroups(t *testing.T) {
	// Setup
	baseManager := &Manager{
		client:      &mockDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	ownershipManager := NewOwnershipManager(baseManager)
	ctx := context.Background()

	// Call the function under test
	err := ownershipManager.TakeoverInactiveGroups(ctx)

	// Verify the results
	assert.NoError(t, err, "TakeoverInactiveGroups should not return an error")
}
