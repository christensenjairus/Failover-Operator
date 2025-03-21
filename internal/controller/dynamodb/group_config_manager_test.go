package dynamodb

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewGroupConfigManager(t *testing.T) {
	// Setup
	baseManager := &Manager{
		client:      &mockDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}

	// Call the function under test
	configManager := NewGroupConfigManager(baseManager)

	// Verify the results
	assert.NotNil(t, configManager, "GroupConfigManager should not be nil")
	assert.Equal(t, baseManager, configManager.Manager, "Base manager should be set correctly")
}

func TestGetGroupConfig(t *testing.T) {
	// Setup
	baseManager := &Manager{
		client:      &mockDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	configManager := NewGroupConfigManager(baseManager)
	ctx := context.Background()
	namespace := "test-namespace"
	name := "test-name"

	// Call the function under test
	config, err := configManager.GetGroupConfig(ctx, namespace, name)

	// Verify the results
	assert.NoError(t, err, "GetGroupConfig should not return an error")
	assert.NotNil(t, config, "Config should not be nil")
	assert.Equal(t, namespace, config.GroupNamespace, "Namespace should match")
	assert.Equal(t, name, config.GroupName, "Name should match")
}

func TestUpdateGroupConfig(t *testing.T) {
	// Setup
	baseManager := &Manager{
		client:      &mockDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	configManager := NewGroupConfigManager(baseManager)
	ctx := context.Background()

	// Get a sample config first
	namespace := "test-namespace"
	name := "test-name"
	config, _ := configManager.GetGroupConfig(ctx, namespace, name)

	// Make some changes
	config.Version++
	config.OwnerCluster = "new-cluster"

	// Call the function under test
	err := configManager.UpdateGroupConfig(ctx, config)

	// Verify the results
	assert.NoError(t, err, "UpdateGroupConfig should not return an error")
}

func TestTransferOwnership(t *testing.T) {
	// Setup
	baseManager := &Manager{
		client:      &mockDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	configManager := NewGroupConfigManager(baseManager)
	ctx := context.Background()
	namespace := "test-namespace"
	name := "test-name"
	newOwner := "new-cluster"

	// Call the function under test
	err := configManager.TransferOwnership(ctx, namespace, name, newOwner)

	// Verify the results
	assert.NoError(t, err, "TransferOwnership should not return an error")
}

func TestUpdateSuspension(t *testing.T) {
	// Setup
	baseManager := &Manager{
		client:      &mockDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	configManager := NewGroupConfigManager(baseManager)
	ctx := context.Background()
	namespace := "test-namespace"
	name := "test-name"
	suspended := true
	reason := "Maintenance"

	// Call the function under test
	err := configManager.UpdateSuspension(ctx, namespace, name, suspended, reason)

	// Verify the results
	assert.NoError(t, err, "UpdateSuspension should not return an error")
}

func TestIsSuspended(t *testing.T) {
	// Setup
	baseManager := &Manager{
		client:      &mockDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	configManager := NewGroupConfigManager(baseManager)
	ctx := context.Background()
	namespace := "test-namespace"
	name := "test-name"

	// Call the function under test
	suspended, reason, err := configManager.IsSuspended(ctx, namespace, name)

	// Verify the results
	assert.NoError(t, err, "IsSuspended should not return an error")
	assert.False(t, suspended, "Default should not be suspended")
	assert.Empty(t, reason, "Reason should be empty when not suspended")
}

func TestGetOwnerCluster(t *testing.T) {
	// Setup
	baseManager := &Manager{
		client:      &mockDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	configManager := NewGroupConfigManager(baseManager)
	ctx := context.Background()
	namespace := "test-namespace"
	name := "test-name"

	// Call the function under test
	owner, err := configManager.GetOwnerCluster(ctx, namespace, name)

	// Verify the results
	assert.NoError(t, err, "GetOwnerCluster should not return an error")
	assert.Equal(t, "test-cluster", owner, "Owner should match the default value")
}

func TestIsOwner(t *testing.T) {
	// Setup
	baseManager := &Manager{
		client:      &mockDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	configManager := NewGroupConfigManager(baseManager)
	ctx := context.Background()
	namespace := "test-namespace"
	name := "test-name"

	// Call the function under test
	isOwner, err := configManager.IsOwner(ctx, namespace, name)

	// Verify the results
	assert.NoError(t, err, "IsOwner should not return an error")
	assert.True(t, isOwner, "Current cluster should be the owner by default")
}
