package dynamodb

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/stretchr/testify/assert"
)

// MockDynamoDBClient is a mock implementation of DynamoDBClient for testing
type MockDynamoDBClient struct {
	GetItemFunc            func(ctx context.Context, params *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	PutItemFunc            func(ctx context.Context, params *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	UpdateItemFunc         func(ctx context.Context, params *dynamodb.UpdateItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
	DeleteItemFunc         func(ctx context.Context, params *dynamodb.DeleteItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error)
	QueryFunc              func(ctx context.Context, params *dynamodb.QueryInput, optFns ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
	ScanFunc               func(ctx context.Context, params *dynamodb.ScanInput, optFns ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error)
	TransactWriteItemsFunc func(ctx context.Context, params *dynamodb.TransactWriteItemsInput, optFns ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error)
	BatchGetItemFunc       func(ctx context.Context, params *dynamodb.BatchGetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.BatchGetItemOutput, error)
	BatchWriteItemFunc     func(ctx context.Context, params *dynamodb.BatchWriteItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.BatchWriteItemOutput, error)
}

func (m *MockDynamoDBClient) GetItem(ctx context.Context, params *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	if m.GetItemFunc != nil {
		return m.GetItemFunc(ctx, params, optFns...)
	}
	return &dynamodb.GetItemOutput{}, nil
}

func (m *MockDynamoDBClient) PutItem(ctx context.Context, params *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	if m.PutItemFunc != nil {
		return m.PutItemFunc(ctx, params, optFns...)
	}
	return &dynamodb.PutItemOutput{}, nil
}

func (m *MockDynamoDBClient) UpdateItem(ctx context.Context, params *dynamodb.UpdateItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	if m.UpdateItemFunc != nil {
		return m.UpdateItemFunc(ctx, params, optFns...)
	}
	return &dynamodb.UpdateItemOutput{}, nil
}

func (m *MockDynamoDBClient) DeleteItem(ctx context.Context, params *dynamodb.DeleteItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	if m.DeleteItemFunc != nil {
		return m.DeleteItemFunc(ctx, params, optFns...)
	}
	return &dynamodb.DeleteItemOutput{}, nil
}

func (m *MockDynamoDBClient) Query(ctx context.Context, params *dynamodb.QueryInput, optFns ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	if m.QueryFunc != nil {
		return m.QueryFunc(ctx, params, optFns...)
	}
	return &dynamodb.QueryOutput{}, nil
}

func (m *MockDynamoDBClient) Scan(ctx context.Context, params *dynamodb.ScanInput, optFns ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
	if m.ScanFunc != nil {
		return m.ScanFunc(ctx, params, optFns...)
	}
	return &dynamodb.ScanOutput{}, nil
}

func (m *MockDynamoDBClient) TransactWriteItems(ctx context.Context, params *dynamodb.TransactWriteItemsInput, optFns ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error) {
	if m.TransactWriteItemsFunc != nil {
		return m.TransactWriteItemsFunc(ctx, params, optFns...)
	}
	return &dynamodb.TransactWriteItemsOutput{}, nil
}

func (m *MockDynamoDBClient) BatchGetItem(ctx context.Context, params *dynamodb.BatchGetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.BatchGetItemOutput, error) {
	if m.BatchGetItemFunc != nil {
		return m.BatchGetItemFunc(ctx, params, optFns...)
	}
	return &dynamodb.BatchGetItemOutput{}, nil
}

func (m *MockDynamoDBClient) BatchWriteItem(ctx context.Context, params *dynamodb.BatchWriteItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.BatchWriteItemOutput, error) {
	if m.BatchWriteItemFunc != nil {
		return m.BatchWriteItemFunc(ctx, params, optFns...)
	}
	return &dynamodb.BatchWriteItemOutput{}, nil
}

// MockStateManager overrides specific methods for testing
type MockStateManager struct {
	*BaseManager
	MockGetGroupConfig func(ctx context.Context, namespace, name string) (*GroupConfigRecord, error)
}

func (m *MockStateManager) GetGroupConfig(ctx context.Context, namespace, name string) (*GroupConfigRecord, error) {
	if m.MockGetGroupConfig != nil {
		return m.MockGetGroupConfig(ctx, namespace, name)
	}
	// Call the base manager directly
	return m.GetGroupConfig(ctx, namespace, name)
}

func TestNewDynamoDBService(t *testing.T) {
	client := &MockDynamoDBClient{}
	service := NewDynamoDBService(client, "test-table", "test-cluster", "test-operator")

	assert.NotNil(t, service)
	assert.NotNil(t, service.volumeStateManager)
	assert.NotNil(t, service.operationsManager)
}

func TestStateManager_GetGroupState(t *testing.T) {
	client := &MockDynamoDBClient{}
	service := NewDynamoDBService(client, "test-table", "test-cluster", "test-operator")

	state, err := service.GetGroupState(context.Background(), "test-namespace", "test-group")

	assert.NoError(t, err)
	assert.NotNil(t, state)
	assert.Equal(t, "test-namespace/test-group", state.GroupID)
}

func TestOperationsManager_ExecuteFailover(t *testing.T) {
	// Create a mock client with the necessary mock functions
	mockClient := &MockDynamoDBClient{
		// Mock for checking lock status and group config and cluster status
		GetItemFunc: func(ctx context.Context, params *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
			// Extract the PK and SK to determine what's being requested
			pk := ""
			sk := ""
			if val, ok := params.Key["PK"]; ok {
				if s, ok := val.(*types.AttributeValueMemberS); ok {
					pk = s.Value
				}
			}
			if val, ok := params.Key["SK"]; ok {
				if s, ok := val.(*types.AttributeValueMemberS); ok {
					sk = s.Value
				}
			}

			// If requesting the config record
			if sk == "CONFIG" {
				// Return a group config with "test-cluster" as owner
				return &dynamodb.GetItemOutput{
					Item: map[string]types.AttributeValue{
						"ownerCluster": &types.AttributeValueMemberS{Value: "test-cluster"},
						"suspended":    &types.AttributeValueMemberBOOL{Value: false},
					},
				}, nil
			}

			// If requesting a cluster status record
			if strings.HasPrefix(sk, "CLUSTER#") {
				clusterName := strings.TrimPrefix(sk, "CLUSTER#")
				return &dynamodb.GetItemOutput{
					Item: map[string]types.AttributeValue{
						"PK":          &types.AttributeValueMemberS{Value: pk},
						"SK":          &types.AttributeValueMemberS{Value: sk},
						"clusterName": &types.AttributeValueMemberS{Value: clusterName},
						"health":      &types.AttributeValueMemberS{Value: string(HealthOK)},
						"state":       &types.AttributeValueMemberS{Value: string(StateStandby)},
					},
				}, nil
			}

			// For lock checks and other GetItem operations, just return empty
			return &dynamodb.GetItemOutput{}, nil
		},
		// Mock for transactions
		TransactWriteItemsFunc: func(ctx context.Context, params *dynamodb.TransactWriteItemsInput, optFns ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error) {
			// Just return a successful response
			return &dynamodb.TransactWriteItemsOutput{}, nil
		},
		// Mock for update operations like history records
		UpdateItemFunc: func(ctx context.Context, params *dynamodb.UpdateItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
			return &dynamodb.UpdateItemOutput{}, nil
		},
		// Mock for put operations
		PutItemFunc: func(ctx context.Context, params *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
			return &dynamodb.PutItemOutput{}, nil
		},
	}

	service := NewDynamoDBService(mockClient, "test-table", "test-cluster", "test-operator")

	// Set up test cases
	testCases := []struct {
		name          string
		targetCluster string
		forceFastMode bool
		expectError   bool
		errorText     string
	}{
		{
			name:          "Same source and target clusters",
			targetCluster: "test-cluster", // Same as the cluster in the service
			forceFastMode: false,
			expectError:   true,
			errorText:     "source and target clusters are the same",
		},
		{
			name:          "Different target - health check respected",
			targetCluster: "different-target-cluster",
			forceFastMode: false,
			expectError:   false,
			errorText:     "",
		},
		{
			name:          "Different target - health check skipped",
			targetCluster: "different-target-cluster",
			forceFastMode: true,
			expectError:   false,
			errorText:     "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := service.ExecuteFailover(
				context.Background(),
				"test-namespace",
				"test-group",
				"test-failover",
				tc.targetCluster,
				tc.name, // Using name as reason
				tc.forceFastMode,
			)

			if tc.expectError {
				assert.Error(t, err)
				if tc.errorText != "" {
					assert.Contains(t, err.Error(), tc.errorText)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestClusterStatusRecord_JsonComponents(t *testing.T) {
	// Test that StatusData can be marshaled and unmarshaled from JSON
	statusData := &StatusData{
		Workloads: []ResourceStatus{
			{
				Kind:   "Deployment",
				Name:   "app",
				Health: HealthOK,
				Status: "Running normally",
			},
		},
		NetworkResources: []ResourceStatus{
			{
				Kind:   "VirtualService",
				Name:   "app-vs",
				Health: HealthOK,
				Status: "Routing traffic correctly",
			},
		},
	}

	client := &MockDynamoDBClient{}
	service := NewDynamoDBService(client, "test-table", "test-cluster", "test-operator")

	// Test marshaling by calling UpdateClusterStatus
	statusJSON, _ := json.Marshal(statusData)
	err := service.UpdateClusterStatus(
		context.Background(),
		"test-namespace",
		"test-group",
		"test-cluster",
		HealthDegraded,
		StatePrimary,
		string(statusJSON),
	)

	assert.NoError(t, err)

	// For backward compatibility, test the legacy function too
	components := map[string]ComponentStatus{
		"database": {
			Health:  HealthOK,
			Message: "Database is healthy",
		},
		"application": {
			Health:  HealthDegraded,
			Message: "Application is running with degraded performance",
		},
	}

	// Test marshaling by calling the legacy function
	err = service.UpdateClusterStatusLegacy(
		context.Background(),
		"test-namespace",
		"test-group",
		HealthDegraded,
		StatePrimary,
		components,
	)

	assert.NoError(t, err)
}

func TestGlobalSecondaryIndex(t *testing.T) {
	mockClient := &MockDynamoDBClient{
		// Mock the GetItem operation for both config and status
		GetItemFunc: func(ctx context.Context, params *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
			// Extract the SK to determine what's being requested
			sk := ""
			if val, ok := params.Key["SK"]; ok {
				if s, ok := val.(*types.AttributeValueMemberS); ok {
					sk = s.Value
				}
			}

			// If requesting the config record
			if sk == "CONFIG" {
				return &dynamodb.GetItemOutput{
					Item: map[string]types.AttributeValue{
						"ownerCluster": &types.AttributeValueMemberS{Value: "test-cluster"},
						"GSI1PK":       &types.AttributeValueMemberS{Value: "OPERATOR#test-operator"},
						"GSI1SK":       &types.AttributeValueMemberS{Value: "GROUP#test-group"},
					},
				}, nil
			}

			// If requesting a cluster status record
			if strings.HasPrefix(sk, "CLUSTER#") {
				return &dynamodb.GetItemOutput{
					Item: map[string]types.AttributeValue{
						"clusterName": &types.AttributeValueMemberS{Value: "test-cluster"},
						"health":      &types.AttributeValueMemberS{Value: string(HealthOK)},
						"state":       &types.AttributeValueMemberS{Value: string(StateStandby)},
						"GSI1PK":      &types.AttributeValueMemberS{Value: "CLUSTER#test-cluster"},
						"GSI1SK":      &types.AttributeValueMemberS{Value: "GROUP#test-namespace#test-group"},
					},
				}, nil
			}

			return &dynamodb.GetItemOutput{}, nil
		},
	}

	service := NewDynamoDBService(mockClient, "test-table", "test-cluster", "test-operator")

	// Get a config record to check GSI fields
	config, err := service.GetGroupConfig(context.Background(), "test-namespace", "test-group")

	assert.NoError(t, err)
	assert.NotEmpty(t, config.GSI1PK)
	assert.NotEmpty(t, config.GSI1SK)
	assert.Contains(t, config.GSI1PK, "OPERATOR#")
	assert.Contains(t, config.GSI1SK, "GROUP#")

	// Get a status record to check GSI fields
	status, err := service.GetClusterStatus(context.Background(), "test-namespace", "test-group", "test-cluster")

	assert.NoError(t, err)
	assert.NotEmpty(t, status.GSI1PK)
	assert.NotEmpty(t, status.GSI1SK)
	assert.Contains(t, status.GSI1PK, "CLUSTER#")
	assert.Contains(t, status.GSI1SK, "GROUP#")
}
