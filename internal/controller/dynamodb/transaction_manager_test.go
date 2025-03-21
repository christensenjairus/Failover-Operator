package dynamodb

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewTransactionManager(t *testing.T) {
	// Setup
	baseManager := &Manager{
		client:      &mockDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}

	// Call the function under test
	transactionManager := NewTransactionManager(baseManager)

	// Verify the results
	assert.NotNil(t, transactionManager, "TransactionManager should not be nil")
	assert.Equal(t, baseManager, transactionManager.Manager, "Base manager should be set correctly")
}

func TestExecuteTransaction(t *testing.T) {
	// Setup
	baseManager := &Manager{
		client:      &mockDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	transactionManager := NewTransactionManager(baseManager)
	ctx := context.Background()
	txType := FailoverTransaction
	operations := []interface{}{}

	// Call the function under test
	err := transactionManager.ExecuteTransaction(ctx, txType, operations)

	// Verify the results
	assert.NoError(t, err, "ExecuteTransaction should not return an error")
}

func TestPrepareFailoverTransaction(t *testing.T) {
	// Setup
	baseManager := &Manager{
		client:      &mockDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	transactionManager := NewTransactionManager(baseManager)
	ctx := context.Background()
	groupID := "test-group"
	fromCluster := "old-cluster"
	toCluster := "new-cluster"
	reason := "test-reason"

	// Call the function under test
	operations, err := transactionManager.PrepareFailoverTransaction(ctx, groupID, fromCluster, toCluster, reason)

	// Verify the results
	assert.NoError(t, err, "PrepareFailoverTransaction should not return an error")
	assert.NotNil(t, operations, "Operations should not be nil")
	assert.NotEmpty(t, operations, "Operations should not be empty")
}
