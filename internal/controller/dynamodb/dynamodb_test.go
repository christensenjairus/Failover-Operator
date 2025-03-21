package dynamodb

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
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

// MockStateManager overrides specific methods for testing
type MockStateManager struct {
	*StateManager
	MockGetGroupConfig func(ctx context.Context, namespace, name string) (*GroupConfigRecord, error)
}

func (m *MockStateManager) GetGroupConfig(ctx context.Context, namespace, name string) (*GroupConfigRecord, error) {
	if m.MockGetGroupConfig != nil {
		return m.MockGetGroupConfig(ctx, namespace, name)
	}
	return m.StateManager.GetGroupConfig(ctx, namespace, name)
}

func TestNewDynamoDBService(t *testing.T) {
	client := &MockDynamoDBClient{}
	service := NewDynamoDBService(client, "test-table", "test-cluster", "test-operator")

	assert.NotNil(t, service)
	assert.NotNil(t, service.State)
	assert.NotNil(t, service.Operations)
}

func TestStateManager_GetGroupState(t *testing.T) {
	client := &MockDynamoDBClient{}
	service := NewDynamoDBService(client, "test-table", "test-cluster", "test-operator")

	state, err := service.State.GetGroupState(context.Background(), "test-namespace", "test-group")

	assert.NoError(t, err)
	assert.NotNil(t, state)
	assert.Equal(t, "test-namespace/test-group", state.GroupID)
}

func TestOperationsManager_ExecuteFailover(t *testing.T) {
	// This simpler test just verifies that non-matching source/target clusters
	// are rejected correctly
	client := &MockDynamoDBClient{}
	service := NewDynamoDBService(client, "test-table", "test-cluster", "test-operator")

	// When source and target are the same, should return error
	err := service.Operations.ExecuteFailover(
		context.Background(),
		"test-namespace",
		"test-group",
		"test-failover",
		"test-cluster", // Same as the cluster in the service (source)
		"Test failover",
		false,
	)

	// Should get an error about source and target being the same
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "source and target clusters are the same")

	// With a different target, it should proceed (mock just returns success)
	err = service.Operations.ExecuteFailover(
		context.Background(),
		"test-namespace",
		"test-group",
		"test-failover",
		"different-target-cluster",
		"Test failover",
		false,
	)

	assert.NoError(t, err)
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
	err := service.State.UpdateClusterStatus(
		context.Background(),
		"test-namespace",
		"test-group",
		HealthDegraded,
		StatePrimary,
		statusData,
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
	err = service.State.UpdateClusterStatusLegacy(
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
	client := &MockDynamoDBClient{}
	service := NewDynamoDBService(client, "test-table", "test-cluster", "test-operator")

	// Get a config record to check GSI fields
	config, err := service.State.GetGroupConfig(context.Background(), "test-namespace", "test-group")

	assert.NoError(t, err)
	assert.NotEmpty(t, config.GSI1PK)
	assert.NotEmpty(t, config.GSI1SK)
	assert.Contains(t, config.GSI1PK, "OPERATOR#")
	assert.Contains(t, config.GSI1SK, "GROUP#")

	// Get a status record to check GSI fields
	status, err := service.State.GetClusterStatus(context.Background(), "test-namespace", "test-group", "test-cluster")

	assert.NoError(t, err)
	assert.NotEmpty(t, status.GSI1PK)
	assert.NotEmpty(t, status.GSI1SK)
	assert.Contains(t, status.GSI1PK, "CLUSTER#")
	assert.Contains(t, status.GSI1SK, "GROUP#")
}
