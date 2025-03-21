package dynamodb

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/stretchr/testify/assert"
)

// TestRemoveClusterStatus verifies the RemoveClusterStatus functionality
func TestRemoveClusterStatus(t *testing.T) {
	// Create a mock client with expected calls
	mockClient := &MockDynamoDBClient{
		DeleteItemFunc: func(ctx context.Context, params *dynamodb.DeleteItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
			// Verify the input parameters
			assert.Equal(t, "test-table", *params.TableName)

			// Check that PK has correct format
			pkValue := params.Key["PK"].(*types.AttributeValueMemberS).Value
			assert.Contains(t, pkValue, "GROUP#test-operator#default#test-group")

			// Check that SK has correct format
			skValue := params.Key["SK"].(*types.AttributeValueMemberS).Value
			assert.Contains(t, skValue, "CLUSTER#test-cluster")

			return &dynamodb.DeleteItemOutput{}, nil
		},
	}

	// Create a base manager with the mock client
	baseManager := &BaseManager{
		client:      mockClient,
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}

	// Create operations manager
	operationsManager := NewOperationsManager(baseManager)

	// Set up test data
	namespace := "default"
	name := "test-group"
	clusterName := "test-cluster"

	// Call the function
	ctx := context.Background()
	err := operationsManager.RemoveClusterStatus(ctx, namespace, name, clusterName)

	// Verify results
	assert.NoError(t, err)
}
