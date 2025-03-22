package dynamodb

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestBaseManagerCreation(t *testing.T) {
	// Setup
	client := &EnhancedTestDynamoDBClient{}
	tableName := "test-table"
	clusterName := "test-cluster"
	operatorID := "test-operator"

	// Call the function under test
	baseManager := &BaseManager{
		client:      client,
		tableName:   tableName,
		clusterName: clusterName,
		operatorID:  operatorID,
	}

	// Verify the results
	assert.NotNil(t, baseManager)
	assert.Equal(t, client, baseManager.client)
	assert.Equal(t, tableName, baseManager.tableName)
	assert.Equal(t, clusterName, baseManager.clusterName)
	assert.Equal(t, operatorID, baseManager.operatorID)
}

func TestGetGroupPK(t *testing.T) {
	// Setup
	baseManager := &BaseManager{
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	namespace := "test-namespace"
	name := "test-name"

	// Call the function under test
	partitionKey := baseManager.getGroupPK(namespace, name)

	// Verify the results
	expectedKey := "GROUP#test-operator#test-namespace#test-name"
	assert.Equal(t, expectedKey, partitionKey)
}

func TestGetClusterSK(t *testing.T) {
	// Setup
	baseManager := &BaseManager{
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	clusterName := "cluster1"

	// Call the function under test
	sortKey := baseManager.getClusterSK(clusterName)

	// Verify the results
	expectedKey := "CLUSTER#cluster1"
	assert.Equal(t, expectedKey, sortKey)
}

func TestGetHistorySK(t *testing.T) {
	// Setup
	baseManager := &BaseManager{
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	timestamp := time.Date(2023, 1, 2, 3, 4, 5, 0, time.UTC)

	// Call the function under test
	sortKey := baseManager.getHistorySK(timestamp)

	// Verify the results
	expectedKey := "HISTORY#2023-01-02T03:04:05Z"
	assert.Equal(t, expectedKey, sortKey)
}

func TestGetOperatorGSI1PK(t *testing.T) {
	// Setup
	baseManager := &BaseManager{
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}

	// Call the function under test
	key := baseManager.getOperatorGSI1PK()

	// Verify the results
	expectedKey := "OPERATOR#test-operator"
	assert.Equal(t, expectedKey, key)
}

func TestGetGroupGSI1SK(t *testing.T) {
	// Setup
	baseManager := &BaseManager{
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	namespace := "test-namespace"
	name := "test-name"

	// Call the function under test
	key := baseManager.getGroupGSI1SK(namespace, name)

	// Verify the results
	expectedKey := "GROUP#test-namespace#test-name"
	assert.Equal(t, expectedKey, key)
}

func TestGetClusterGSI1PK(t *testing.T) {
	// Setup
	baseManager := &BaseManager{
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	clusterName := "cluster1"

	// Call the function under test
	key := baseManager.getClusterGSI1PK(clusterName)

	// Verify the results
	expectedKey := "CLUSTER#cluster1"
	assert.Equal(t, expectedKey, key)
}
