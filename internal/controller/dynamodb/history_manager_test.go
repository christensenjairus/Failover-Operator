package dynamodb

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewHistoryManager(t *testing.T) {
	// Setup
	baseManager := &Manager{
		client:      &mockDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}

	// Call the function under test
	historyManager := NewHistoryManager(baseManager)

	// Verify the results
	assert.NotNil(t, historyManager, "HistoryManager should not be nil")
	assert.Equal(t, baseManager, historyManager.Manager, "Base manager should be set correctly")
}

func TestRecordFailoverEvent(t *testing.T) {
	// Setup
	baseManager := &Manager{
		client:      &mockDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	historyManager := NewHistoryManager(baseManager)
	ctx := context.Background()
	groupID := "test-group"
	fromCluster := "old-cluster"
	toCluster := "new-cluster"
	reason := "test-reason"

	// Call the function under test
	err := historyManager.RecordFailoverEvent(ctx, groupID, fromCluster, toCluster, reason)

	// Verify the results
	assert.NoError(t, err, "RecordFailoverEvent should not return an error")
}

func TestGetFailoverHistory(t *testing.T) {
	// Setup
	baseManager := &Manager{
		client:      &mockDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	historyManager := NewHistoryManager(baseManager)
	ctx := context.Background()
	groupID := "test-group"
	limit := 10

	// Call the function under test
	history, err := historyManager.GetFailoverHistory(ctx, groupID, limit)

	// Verify the results
	assert.NoError(t, err, "GetFailoverHistory should not return an error")
	assert.NotNil(t, history, "History should not be nil")
}
