package dynamodb

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewConfigManager(t *testing.T) {
	// Setup
	baseManager := &Manager{
		client:      &mockDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}

	// Call the function under test
	configManager := NewConfigManager(baseManager)

	// Verify the results
	assert.NotNil(t, configManager, "ConfigManager should not be nil")
	assert.Equal(t, baseManager, configManager.Manager, "Base manager should be set correctly")
}

func TestGetConfig(t *testing.T) {
	// Setup
	baseManager := &Manager{
		client:      &mockDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	configManager := NewConfigManager(baseManager)
	ctx := context.Background()
	groupID := "test-group"

	// Call the function under test
	config, err := configManager.GetConfig(ctx, groupID)

	// Verify the results
	assert.NoError(t, err, "GetConfig should not return an error")
	assert.NotNil(t, config, "Config should not be nil")
	assert.NotEmpty(t, config, "Config should not be empty")
	assert.Contains(t, config, "failoverThreshold", "Config should contain failoverThreshold")
	assert.Contains(t, config, "heartbeatInterval", "Config should contain heartbeatInterval")
	assert.Contains(t, config, "failoverTimeout", "Config should contain failoverTimeout")
	assert.Contains(t, config, "healthCheckInterval", "Config should contain healthCheckInterval")
}

func TestUpdateConfig(t *testing.T) {
	// Setup
	baseManager := &Manager{
		client:      &mockDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	configManager := NewConfigManager(baseManager)
	ctx := context.Background()
	groupID := "test-group"
	config := map[string]interface{}{
		"failoverThreshold":   5,
		"heartbeatInterval":   60,
		"failoverTimeout":     600,
		"healthCheckInterval": 30,
	}

	// Call the function under test
	err := configManager.UpdateConfig(ctx, groupID, config)

	// Verify the results
	assert.NoError(t, err, "UpdateConfig should not return an error")
}
