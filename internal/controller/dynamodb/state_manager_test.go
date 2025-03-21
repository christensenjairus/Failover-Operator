package dynamodb

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/stretchr/testify/assert"
)

func TestNewStateManager(t *testing.T) {
	// Setup
	baseManager := &BaseManager{
		client:      &EnhancedTestDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}

	// Call the function under test
	stateManager := NewStateManager(baseManager)

	// Verify the results
	assert.NotNil(t, stateManager, "StateManager should not be nil")
	assert.Equal(t, baseManager, stateManager.BaseManager, "Base manager should be set correctly")
}

func TestGetGroupState(t *testing.T) {
	// Setup
	baseManager := &BaseManager{
		client:      &EnhancedTestDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	stateManager := NewStateManager(baseManager)
	ctx := context.Background()
	namespace := "test-namespace"
	name := "test-name"

	// Call the function under test
	state, err := stateManager.GetGroupState(ctx, namespace, name)

	// Verify the results
	assert.NoError(t, err, "GetGroupState should not return an error")
	assert.NotNil(t, state, "State should not be nil")
	assert.Equal(t, namespace+"/"+name, state.GroupID, "GroupID should match")
}

func TestGetGroupConfig(t *testing.T) {
	// Setup
	baseManager := &BaseManager{
		client:      &EnhancedTestDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	stateManager := NewStateManager(baseManager)
	ctx := context.Background()
	namespace := "test-namespace"
	name := "test-name"

	// Call the function under test
	config, err := stateManager.GetGroupConfig(ctx, namespace, name)

	// Verify the results
	assert.NoError(t, err, "GetGroupConfig should not return an error")
	assert.NotNil(t, config, "Config should not be nil")
	assert.Equal(t, namespace, config.GroupNamespace, "Namespace should match")
	assert.Equal(t, name, config.GroupName, "Name should match")
	assert.Equal(t, baseManager.operatorID, config.OperatorID, "OperatorID should match")

	// Check GSI fields are set correctly
	assert.Equal(t, baseManager.getOperatorGSI1PK(), config.GSI1PK, "GSI1PK should match")
	assert.Equal(t, baseManager.getGroupGSI1SK(namespace, name), config.GSI1SK, "GSI1SK should match")
}

func TestUpdateGroupConfig(t *testing.T) {
	// Setup
	baseManager := &BaseManager{
		client:      &EnhancedTestDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	stateManager := NewStateManager(baseManager)
	ctx := context.Background()
	config := &GroupConfigRecord{
		PK:             baseManager.getGroupPK("test-namespace", "test-name"),
		SK:             "CONFIG",
		GSI1PK:         baseManager.getOperatorGSI1PK(),
		GSI1SK:         baseManager.getGroupGSI1SK("test-namespace", "test-name"),
		OperatorID:     baseManager.operatorID,
		GroupNamespace: "test-namespace",
		GroupName:      "test-name",
		OwnerCluster:   baseManager.clusterName,
		Version:        1,
		LastUpdated:    time.Now(),
	}

	// Call the function under test
	err := stateManager.UpdateGroupConfig(ctx, config)

	// Verify the results
	assert.NoError(t, err, "UpdateGroupConfig should not return an error")
}

func TestGetClusterStatus(t *testing.T) {
	// Setup
	mockClient := &MockDynamoDBClient{
		GetItemFunc: func(ctx context.Context, params *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
			// Verify the input parameters
			pk, ok := params.Key["PK"]
			assert.True(t, ok, "PK key should be present")

			sk, ok := params.Key["SK"]
			assert.True(t, ok, "SK key should be present")

			// Return a mock cluster status
			return &dynamodb.GetItemOutput{
				Item: map[string]types.AttributeValue{
					"PK":             &types.AttributeValueMemberS{Value: pk.(*types.AttributeValueMemberS).Value},
					"SK":             &types.AttributeValueMemberS{Value: sk.(*types.AttributeValueMemberS).Value},
					"operatorID":     &types.AttributeValueMemberS{Value: "test-operator"},
					"groupNamespace": &types.AttributeValueMemberS{Value: "test-namespace"},
					"groupName":      &types.AttributeValueMemberS{Value: "test-name"},
					"clusterName":    &types.AttributeValueMemberS{Value: "test-cluster"},
					"health":         &types.AttributeValueMemberS{Value: string(HealthOK)},
					"state":          &types.AttributeValueMemberS{Value: string(StateStandby)},
					"lastHeartbeat":  &types.AttributeValueMemberS{Value: time.Now().Format(time.RFC3339)},
					"GSI1PK":         &types.AttributeValueMemberS{Value: "CLUSTER#test-cluster"},
					"GSI1SK":         &types.AttributeValueMemberS{Value: "GROUP#test-namespace#test-name"},
				},
			}, nil
		},
	}

	baseManager := NewBaseManager(mockClient, "test-table", "test-cluster", "test-operator")
	stateManager := NewStateManager(baseManager)
	ctx := context.Background()
	namespace := "test-namespace"
	name := "test-name"
	clusterName := "test-cluster"

	// Call the function under test
	status, err := stateManager.GetClusterStatus(ctx, namespace, name, clusterName)

	// Verify the results
	assert.NoError(t, err, "GetClusterStatus should not return an error")
	assert.NotNil(t, status, "Status should not be nil")
	assert.Equal(t, namespace, status.GroupNamespace, "Namespace should match")
	assert.Equal(t, name, status.GroupName, "Name should match")
	assert.Equal(t, clusterName, status.ClusterName, "ClusterName should match")
	assert.Equal(t, baseManager.operatorID, status.OperatorID, "OperatorID should match")

	// Check GSI fields are set correctly
	assert.Equal(t, baseManager.getClusterGSI1PK(clusterName), status.GSI1PK, "GSI1PK should match")
	assert.Equal(t, baseManager.getGroupGSI1SK(namespace, name), status.GSI1SK, "GSI1SK should match")
}

func TestUpdateClusterStatus(t *testing.T) {
	// Mock client
	client := &MockDynamoDBClient{}
	baseManager := NewBaseManager(client, "test-table", "test-cluster", "test-operator")
	stateManager := NewStateManager(baseManager)

	// Test context
	ctx := context.Background()

	// Test parameters
	namespace := "test-namespace"
	name := "test-group"
	health := "OK"
	state := "PRIMARY"

	// Test the function
	err := stateManager.UpdateClusterStatus(ctx, namespace, name, "test-cluster", health, state, "{}")

	// Check result
	assert.NoError(t, err, "UpdateClusterStatus should not return an error")
}

// For backward compatibility testing
func TestUpdateClusterStatusLegacy(t *testing.T) {
	// Mock client
	client := &MockDynamoDBClient{}
	baseManager := NewBaseManager(client, "test-table", "test-cluster", "test-operator")
	stateManager := NewStateManager(baseManager)

	// Test context
	ctx := context.Background()

	// Test parameters
	namespace := "test-namespace"
	name := "test-group"
	health := "OK"
	state := "PRIMARY"

	// Create legacy components map for testing
	components := map[string]ComponentStatus{
		"web-app": {
			Health:  "OK",
			Message: "Web app is healthy",
		},
		"database": {
			Health:  "OK",
			Message: "Database is healthy",
		},
	}

	// Test the function
	err := stateManager.UpdateClusterStatusLegacy(ctx, namespace, name, health, state, components)

	// Check result
	assert.NoError(t, err, "UpdateClusterStatusLegacy should not return an error")
}

func TestGetAllClusterStatuses(t *testing.T) {
	// Setup
	mockClient := &MockDynamoDBClient{
		// Mock query operation to return cluster statuses
		QueryFunc: func(ctx context.Context, params *dynamodb.QueryInput, optFns ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
			// Return cluster statuses
			return &dynamodb.QueryOutput{
				Items: []map[string]types.AttributeValue{
					{
						"PK":          &types.AttributeValueMemberS{Value: "GROUP#test-operator#test-namespace#test-name"},
						"SK":          &types.AttributeValueMemberS{Value: "CLUSTER#test-cluster"},
						"operatorID":  &types.AttributeValueMemberS{Value: "test-operator"},
						"clusterName": &types.AttributeValueMemberS{Value: "test-cluster"},
						"health":      &types.AttributeValueMemberS{Value: string(HealthOK)},
						"state":       &types.AttributeValueMemberS{Value: string(StatePrimary)},
					},
				},
			}, nil
		},
	}

	baseManager := NewBaseManager(mockClient, "test-table", "test-cluster", "test-operator")
	stateManager := NewStateManager(baseManager)
	ctx := context.Background()
	namespace := "test-namespace"
	name := "test-name"

	// Call the function under test
	statuses, err := stateManager.GetAllClusterStatuses(ctx, namespace, name)

	// Verify the results
	assert.NoError(t, err, "GetAllClusterStatuses should not return an error")
	assert.NotNil(t, statuses, "Statuses should not be nil")
	assert.NotEmpty(t, statuses, "Statuses should not be empty")
	assert.Contains(t, statuses, baseManager.clusterName, "Statuses should contain the current cluster")
}

func TestGetFailoverHistory(t *testing.T) {
	// Setup
	mockClient := &MockDynamoDBClient{
		// Mock query operation to return history records
		QueryFunc: func(ctx context.Context, params *dynamodb.QueryInput, optFns ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
			// Check if this is a query for history records
			keyCondition := aws.ToString(params.KeyConditionExpression)
			if strings.Contains(keyCondition, "begins_with") {
				// Return a history entry
				return &dynamodb.QueryOutput{
					Items: []map[string]types.AttributeValue{
						{
							"PK":             &types.AttributeValueMemberS{Value: "GROUP#test-operator#test-namespace#test-name"},
							"SK":             &types.AttributeValueMemberS{Value: "HISTORY#20230101120000"},
							"operatorID":     &types.AttributeValueMemberS{Value: "test-operator"},
							"groupNamespace": &types.AttributeValueMemberS{Value: "test-namespace"},
							"groupName":      &types.AttributeValueMemberS{Value: "test-name"},
							"failoverName":   &types.AttributeValueMemberS{Value: "Failover-1"},
							"sourceCluster":  &types.AttributeValueMemberS{Value: "source-cluster"},
							"targetCluster":  &types.AttributeValueMemberS{Value: "target-cluster"},
							"reason":         &types.AttributeValueMemberS{Value: "Test failover"},
							"startTime":      &types.AttributeValueMemberS{Value: "2023-01-01T12:00:00Z"},
							"endTime":        &types.AttributeValueMemberS{Value: "2023-01-01T12:01:00Z"},
							"status":         &types.AttributeValueMemberS{Value: "SUCCESS"},
							"downtime":       &types.AttributeValueMemberN{Value: "30"},
							"duration":       &types.AttributeValueMemberN{Value: "60"},
						},
					},
				}, nil
			}
			// Return empty result for other queries
			return &dynamodb.QueryOutput{}, nil
		},
	}

	baseManager := NewBaseManager(mockClient, "test-table", "test-cluster", "test-operator")
	stateManager := NewStateManager(baseManager)
	ctx := context.Background()
	namespace := "test-namespace"
	name := "test-name"
	limit := 1

	// Call the function under test
	history, err := stateManager.GetFailoverHistory(ctx, namespace, name, limit)

	// Verify the results
	assert.NoError(t, err, "GetFailoverHistory should not return an error")
	assert.NotNil(t, history, "History should not be nil")
	assert.Len(t, history, limit, "History should have exactly one element with limit=1")
	assert.Equal(t, "Failover-1", history[0].FailoverName, "FailoverName should match the expected value")
}

func TestSyncClusterState(t *testing.T) {
	// Setup
	baseManager := &BaseManager{
		client:      &EnhancedTestDynamoDBClient{},
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	stateManager := NewStateManager(baseManager)
	ctx := context.Background()
	namespace := "test-namespace"
	name := "test-name"

	// Call the function under test
	err := stateManager.SyncClusterState(ctx, namespace, name)

	// Verify the results
	assert.NoError(t, err, "SyncClusterState should not return an error")
}

func TestDetectStaleHeartbeats(t *testing.T) {
	// Setup
	mockClient := &EnhancedTestDynamoDBClient{
		StaleClustersReturnFn: func() []string {
			return []string{"stale-cluster-1"}
		},
	}

	baseManager := &BaseManager{
		client:      mockClient,
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}
	stateManager := NewStateManager(baseManager)
	ctx := context.Background()
	namespace := "test-namespace"
	name := "test-name"

	// Call the function under test
	staleClusters, err := stateManager.DetectStaleHeartbeats(ctx, namespace, name)

	// Verify the results
	assert.NoError(t, err, "DetectStaleHeartbeats should not return an error")
	assert.NotNil(t, staleClusters, "StaleClusters should not be nil even if empty")
	assert.Contains(t, staleClusters, "stale-cluster-1", "Should include the mocked stale cluster")
}

// TestDeleteGroupConfig verifies the DeleteGroupConfig functionality
func TestDeleteGroupConfig(t *testing.T) {
	// Create a mock client
	mockClient := &MockDynamoDBClient{
		DeleteItemFunc: func(ctx context.Context, params *dynamodb.DeleteItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
			// Verify the input parameters
			assert.Equal(t, "test-table", *params.TableName)
			assert.Contains(t, params.Key["PK"].(*types.AttributeValueMemberS).Value, "default")
			assert.Contains(t, params.Key["PK"].(*types.AttributeValueMemberS).Value, "test-group")
			assert.Equal(t, "CONFIG", params.Key["SK"].(*types.AttributeValueMemberS).Value)
			return &dynamodb.DeleteItemOutput{}, nil
		},
	}

	// Create a base manager with the mock client
	manager := &BaseManager{
		client:      mockClient,
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}

	// Set up test data
	namespace := "default"
	name := "test-group"

	// Call the function
	ctx := context.Background()
	err := manager.DeleteGroupConfig(ctx, namespace, name)

	// Verify results
	assert.NoError(t, err)
}

// TestDeleteAllHistoryRecords verifies the DeleteAllHistoryRecords functionality
func TestDeleteAllHistoryRecords(t *testing.T) {
	// Create a mock client with expected calls
	mockClient := &MockDynamoDBClient{
		QueryFunc: func(ctx context.Context, params *dynamodb.QueryInput, optFns ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
			// Verify query parameters
			assert.Equal(t, "test-table", *params.TableName)
			assert.Contains(t, *params.KeyConditionExpression, "begins_with(SK, :sk_prefix)")

			// Return mock history records
			return &dynamodb.QueryOutput{
				Items: []map[string]types.AttributeValue{
					{
						"PK": &types.AttributeValueMemberS{Value: "GROUP#test-operator#default#test-group"},
						"SK": &types.AttributeValueMemberS{Value: "HISTORY#20230101120000"},
					},
					{
						"PK": &types.AttributeValueMemberS{Value: "GROUP#test-operator#default#test-group"},
						"SK": &types.AttributeValueMemberS{Value: "HISTORY#20230101120001"},
					},
				},
			}, nil
		},
		DeleteItemFunc: func(ctx context.Context, params *dynamodb.DeleteItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
			// Verify delete parameters
			assert.Equal(t, "test-table", *params.TableName)
			assert.Contains(t, params.Key["PK"].(*types.AttributeValueMemberS).Value, "GROUP#test-operator#default#test-group")
			assert.Contains(t, params.Key["SK"].(*types.AttributeValueMemberS).Value, "HISTORY#")

			return &dynamodb.DeleteItemOutput{}, nil
		},
	}

	// Create a base manager with the mock client
	manager := &BaseManager{
		client:      mockClient,
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}

	// Set up test data
	namespace := "default"
	name := "test-group"

	// Call the function
	ctx := context.Background()
	err := manager.DeleteAllHistoryRecords(ctx, namespace, name)

	// Verify results
	assert.NoError(t, err)
}

// TestDeleteAllClusterStatuses verifies the DeleteAllClusterStatuses functionality
func TestDeleteAllClusterStatuses(t *testing.T) {
	// Create a mock client with expected calls
	mockClient := &MockDynamoDBClient{
		QueryFunc: func(ctx context.Context, params *dynamodb.QueryInput, optFns ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
			// Verify query parameters
			assert.Equal(t, "test-table", *params.TableName)
			assert.Contains(t, *params.KeyConditionExpression, "begins_with(SK, :sk_prefix)")

			// Return mock cluster status records
			return &dynamodb.QueryOutput{
				Items: []map[string]types.AttributeValue{
					{
						"PK": &types.AttributeValueMemberS{Value: "GROUP#test-operator#default#test-group"},
						"SK": &types.AttributeValueMemberS{Value: "CLUSTER#cluster1"},
					},
					{
						"PK": &types.AttributeValueMemberS{Value: "GROUP#test-operator#default#test-group"},
						"SK": &types.AttributeValueMemberS{Value: "CLUSTER#cluster2"},
					},
				},
			}, nil
		},
		DeleteItemFunc: func(ctx context.Context, params *dynamodb.DeleteItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
			// Verify delete parameters
			assert.Equal(t, "test-table", *params.TableName)
			assert.Contains(t, params.Key["PK"].(*types.AttributeValueMemberS).Value, "GROUP#test-operator#default#test-group")
			assert.Contains(t, params.Key["SK"].(*types.AttributeValueMemberS).Value, "CLUSTER#")

			return &dynamodb.DeleteItemOutput{}, nil
		},
	}

	// Create a base manager with the mock client
	manager := &BaseManager{
		client:      mockClient,
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}

	// Set up test data
	namespace := "default"
	name := "test-group"

	// Call the function
	ctx := context.Background()
	err := manager.DeleteAllClusterStatuses(ctx, namespace, name)

	// Verify results
	assert.NoError(t, err)
}

// TestDeleteLock verifies the DeleteLock functionality
func TestDeleteLock(t *testing.T) {
	// Create a mock client with expected calls
	mockClient := &MockDynamoDBClient{
		DeleteItemFunc: func(ctx context.Context, params *dynamodb.DeleteItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
			// Verify the input parameters
			assert.Equal(t, "test-table", *params.TableName)
			assert.Contains(t, params.Key["PK"].(*types.AttributeValueMemberS).Value, "GROUP#test-operator#default#test-group")
			assert.Equal(t, "LOCK", params.Key["SK"].(*types.AttributeValueMemberS).Value)

			return &dynamodb.DeleteItemOutput{}, nil
		},
	}

	// Create a base manager with the mock client
	manager := &BaseManager{
		client:      mockClient,
		tableName:   "test-table",
		clusterName: "test-cluster",
		operatorID:  "test-operator",
	}

	// Set up test data
	namespace := "default"
	name := "test-group"

	// Call the function
	ctx := context.Background()
	err := manager.DeleteLock(ctx, namespace, name)

	// Verify results
	assert.NoError(t, err)
}
