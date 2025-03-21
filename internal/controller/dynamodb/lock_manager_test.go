package dynamodb

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewLockManager(t *testing.T) {
	// Setup
	baseManager := &Manager{
		client:      &mockDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}

	// Call the function under test
	lockManager := NewLockManager(baseManager)

	// Verify the results
	assert.NotNil(t, lockManager, "LockManager should not be nil")
	assert.Equal(t, baseManager, lockManager.Manager, "Base manager should be set correctly")
}

func TestAcquireLock(t *testing.T) {
	// Setup
	baseManager := &Manager{
		client:      &mockDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	lockManager := NewLockManager(baseManager)
	ctx := context.Background()
	groupID := "test-group"
	duration := 10 * time.Second

	// Call the function under test
	acquired, err := lockManager.AcquireLock(ctx, groupID, duration)

	// Verify the results
	assert.NoError(t, err, "AcquireLock should not return an error")
	assert.True(t, acquired, "Lock should be acquired")
}

func TestReleaseLock(t *testing.T) {
	// Setup
	baseManager := &Manager{
		client:      &mockDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	lockManager := NewLockManager(baseManager)
	ctx := context.Background()
	groupID := "test-group"

	// Call the function under test
	err := lockManager.ReleaseLock(ctx, groupID)

	// Verify the results
	assert.NoError(t, err, "ReleaseLock should not return an error")
}

func TestCleanupExpiredLocks(t *testing.T) {
	// Setup
	baseManager := &Manager{
		client:      &mockDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	lockManager := NewLockManager(baseManager)
	ctx := context.Background()

	// Call the function under test
	err := lockManager.CleanupExpiredLocks(ctx)

	// Verify the results
	assert.NoError(t, err, "CleanupExpiredLocks should not return an error")
}
