package dynamodb

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

// These tests require a running local DynamoDB instance
// Before running, make sure you have started the local DynamoDB using:
// ./scripts/setup-local-dynamodb.sh
//
// These tests are designed to run against a real DynamoDB instance (local or remote)
// They're marked as integration tests and will be skipped unless you set:
// ENABLE_INTEGRATION_TESTS=true

func init() {
	// Set up logging for tests
	logf.SetLogger(zap.New(zap.UseDevMode(true)))
}

// getTestDynamoDBClient creates a DynamoDB client for integration testing
func getTestDynamoDBClient(t *testing.T) *dynamodb.Client {
	if os.Getenv("ENABLE_INTEGRATION_TESTS") != "true" {
		t.Skip("Skipping integration test. Set ENABLE_INTEGRATION_TESTS=true to run.")
	}

	// Set up default local DynamoDB environment if not already set
	if os.Getenv("AWS_ENDPOINT") == "" {
		os.Setenv("AWS_ENDPOINT", "http://localhost:8000")
		os.Setenv("AWS_REGION", "us-west-2")
		os.Setenv("AWS_ACCESS_KEY_ID", "local")
		os.Setenv("AWS_SECRET_ACCESS_KEY", "local")
		os.Setenv("AWS_USE_LOCAL_ENDPOINT", "true")
	}

	// Create client
	ctx := context.Background()
	client, err := CreateAWSDynamoDBClient(ctx)
	require.NoError(t, err, "Failed to create DynamoDB client")

	// Type assertion to get the concrete *dynamodb.Client
	dynamoClient, ok := client.(*dynamodb.Client)
	require.True(t, ok, "Client is not a *dynamodb.Client")

	return dynamoClient
}

// setupTestTable creates a test table with a unique name and returns the table name
func setupTestTable(t *testing.T, client *dynamodb.Client) string {
	tableName := "failover-operator-test-" + time.Now().Format("20060102150405")

	ctx := context.Background()

	// Create test table
	options := &TableSetupOptions{
		TableName:     tableName,
		BillingMode:   types.BillingModePayPerRequest,
		WaitForActive: true,
	}

	err := SetupDynamoDBTable(ctx, client, options)
	require.NoError(t, err, "Failed to create test table")

	t.Cleanup(func() {
		// Clean up the test table
		_, err := client.DeleteTable(context.Background(), &dynamodb.DeleteTableInput{
			TableName: aws.String(tableName),
		})
		if err != nil {
			t.Logf("Failed to delete test table: %v", err)
		}
	})

	return tableName
}

// TestDynamoDBIntegration_FullCycle tests a complete failover cycle
func TestDynamoDBIntegration_FullCycle(t *testing.T) {
	// Get DynamoDB client
	client := getTestDynamoDBClient(t)

	// Set up test table
	tableName := setupTestTable(t, client)

	// Set up DynamoDB service with the test table
	service := NewDynamoDBService(client, tableName, "cluster-a", "test-operator")

	// Test context
	ctx := context.Background()
	namespace := "default"
	name := "test-group"

	// 1. Get initial group config (should create default)
	config, err := service.BaseManager.GetGroupConfig(ctx, namespace, name)
	require.NoError(t, err, "Failed to get initial group config")
	assert.Equal(t, "cluster-a", config.OwnerCluster, "Initial owner should be cluster-a")
	assert.Equal(t, 1, config.Version, "Initial version should be 1")

	// 2. Update cluster status for both clusters
	err = service.UpdateClusterStatus(ctx, namespace, name, HealthOK, StatePrimary, nil)
	require.NoError(t, err, "Failed to update cluster-a status")

	err = service.UpdateClusterStatus(ctx, namespace, name, HealthOK, StateStandby, nil)
	require.NoError(t, err, "Failed to update cluster-b status")

	// 3. Get all cluster statuses
	statuses, err := service.GetAllClusterStatuses(ctx, namespace, name)
	require.NoError(t, err, "Failed to get all cluster statuses")
	assert.Len(t, statuses, 2, "Should have 2 cluster statuses")
	assert.Contains(t, statuses, "cluster-a", "Should have status for cluster-a")
	assert.Contains(t, statuses, "cluster-b", "Should have status for cluster-b")

	// 4. Execute failover from cluster-a to cluster-b
	err = service.ExecuteFailover(ctx, namespace, name, "test-failover", "cluster-b", "integration test", false)
	require.NoError(t, err, "Failed to execute failover")

	// 5. Verify ownership changed
	updatedConfig, err := service.BaseManager.GetGroupConfig(ctx, namespace, name)
	require.NoError(t, err, "Failed to get updated group config")
	assert.Equal(t, "cluster-b", updatedConfig.OwnerCluster, "Owner should now be cluster-b")
	assert.Equal(t, "cluster-a", updatedConfig.PreviousOwner, "Previous owner should be cluster-a")
	assert.Equal(t, 2, updatedConfig.Version, "Version should be incremented")
	assert.NotNil(t, updatedConfig.LastFailover, "LastFailover should be set")

	// 6. Verify cluster states changed
	updatedStatuses, err := service.GetAllClusterStatuses(ctx, namespace, name)
	require.NoError(t, err, "Failed to get updated cluster statuses")

	assert.Equal(t, StateFailover, updatedStatuses["cluster-a"].State, "cluster-a should be in FAILOVER state")
	assert.Equal(t, StatePrimary, updatedStatuses["cluster-b"].State, "cluster-b should be in PRIMARY state")

	// 7. Get failover history
	history, err := service.BaseManager.GetFailoverHistory(ctx, namespace, name, 10)
	require.NoError(t, err, "Failed to get failover history")
	assert.GreaterOrEqual(t, len(history), 1, "Should have at least one history record")

	if len(history) > 0 {
		assert.Equal(t, "cluster-a", history[0].SourceCluster, "Source cluster should be cluster-a")
		assert.Equal(t, "cluster-b", history[0].TargetCluster, "Target cluster should be cluster-b")
		assert.Equal(t, "test-failover", history[0].FailoverName, "Failover name should match")
		assert.Equal(t, "integration test", history[0].Reason, "Reason should match")
	}
}

// TestDynamoDBIntegration_VolumeState tests the volume state tracking
func TestDynamoDBIntegration_VolumeState(t *testing.T) {
	// Get DynamoDB client
	client := getTestDynamoDBClient(t)

	// Set up test table
	tableName := setupTestTable(t, client)

	// Set up DynamoDB service with the test table
	service := NewDynamoDBService(client, tableName, "cluster-a", "test-operator")

	// Test context
	ctx := context.Background()
	namespace := "default"
	name := "test-group"

	// 1. Set initial volume state
	err := service.SetVolumeState(ctx, namespace, name, VolumeStateReady)
	require.NoError(t, err, "Failed to set initial volume state")

	// 2. Get volume state
	state, err := service.GetVolumeState(ctx, namespace, name)
	require.NoError(t, err, "Failed to get volume state")
	assert.Equal(t, VolumeStateReady, state, "Volume state should match")

	// 3. Update volume state
	err = service.SetVolumeState(ctx, namespace, name, VolumeStatePromoted)
	require.NoError(t, err, "Failed to update volume state")

	// 4. Get updated volume state
	updatedState, err := service.GetVolumeState(ctx, namespace, name)
	require.NoError(t, err, "Failed to get updated volume state")
	assert.Equal(t, VolumeStatePromoted, updatedState, "Updated volume state should match")

	// 5. Remove volume state
	err = service.RemoveVolumeState(ctx, namespace, name)
	require.NoError(t, err, "Failed to remove volume state")

	// 6. Get volume state after removal
	emptyState, err := service.GetVolumeState(ctx, namespace, name)
	require.NoError(t, err, "Failed to get volume state after removal")
	assert.Equal(t, "", emptyState, "Volume state should be empty after removal")
}

// TestDynamoDBIntegration_Heartbeats tests the heartbeat functionality
func TestDynamoDBIntegration_Heartbeats(t *testing.T) {
	// Get DynamoDB client
	client := getTestDynamoDBClient(t)

	// Set up test table
	tableName := setupTestTable(t, client)

	// Set up DynamoDB service with the test table
	service := NewDynamoDBService(client, tableName, "cluster-a", "test-operator")

	// Test context
	ctx := context.Background()
	namespace := "default"
	name := "test-group"

	// 1. Update heartbeat
	err := service.UpdateHeartbeat(ctx, namespace, name, "cluster-a")
	require.NoError(t, err, "Failed to update heartbeat")

	// 2. Get cluster status to verify heartbeat
	status, err := service.BaseManager.GetClusterStatus(ctx, namespace, name, "cluster-a")
	require.NoError(t, err, "Failed to get cluster status")
	assert.NotEqual(t, time.Time{}, status.LastHeartbeat, "Heartbeat timestamp should be set")

	// Store the initial heartbeat time
	initialHeartbeat := status.LastHeartbeat

	// Wait a moment to ensure timestamp changes
	time.Sleep(1 * time.Second)

	// 3. Update heartbeat again
	err = service.UpdateHeartbeat(ctx, namespace, name, "cluster-a")
	require.NoError(t, err, "Failed to update heartbeat again")

	// 4. Get updated cluster status
	updatedStatus, err := service.BaseManager.GetClusterStatus(ctx, namespace, name, "cluster-a")
	require.NoError(t, err, "Failed to get updated cluster status")
	assert.True(t, updatedStatus.LastHeartbeat.After(initialHeartbeat), "Heartbeat should be updated to a later time")

	// 5. Test stale heartbeat detection
	// First, add a second cluster with an old heartbeat
	oldTime := time.Now().Add(-10 * time.Minute)
	oldStatus := &ClusterStatusRecord{
		PK:             service.BaseManager.getGroupPK(namespace, name),
		SK:             service.BaseManager.getClusterSK("cluster-stale"),
		GSI1PK:         service.BaseManager.getClusterGSI1PK("cluster-stale"),
		GSI1SK:         service.BaseManager.getGroupGSI1SK(namespace, name),
		OperatorID:     service.OperatorID,
		GroupNamespace: namespace,
		GroupName:      name,
		ClusterName:    "cluster-stale",
		Health:         HealthOK,
		State:          StateStandby,
		LastHeartbeat:  oldTime,
		Components:     "{}",
	}

	// Manually insert the record with old timestamp
	item, err := marshalDynamoDBItem(oldStatus)
	require.NoError(t, err, "Failed to marshal stale cluster record")

	_, err = client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(tableName),
		Item:      item,
	})
	require.NoError(t, err, "Failed to insert stale cluster record")

	// 6. Detect stale heartbeats
	staleClusters, err := service.DetectStaleHeartbeats(ctx, namespace, name)
	require.NoError(t, err, "Failed to detect stale heartbeats")
	assert.Contains(t, staleClusters, "cluster-stale", "Should detect stale cluster")
	assert.NotContains(t, staleClusters, "cluster-a", "Should not detect non-stale cluster")
}

// Helper function to marshal a struct to DynamoDB item format
func marshalDynamoDBItem(item interface{}) (map[string]types.AttributeValue, error) {
	// This is a simplified implementation - in a real scenario you might want to use
	// marshalling libraries like dynamodbattribute from github.com/aws/aws-sdk-go

	// Here we'll return a simple implementation for the ClusterStatusRecord type
	record, ok := item.(*ClusterStatusRecord)
	if !ok {
		return nil, nil
	}

	result := make(map[string]types.AttributeValue)

	// Add all fields
	result["PK"] = &types.AttributeValueMemberS{Value: record.PK}
	result["SK"] = &types.AttributeValueMemberS{Value: record.SK}
	result["GSI1PK"] = &types.AttributeValueMemberS{Value: record.GSI1PK}
	result["GSI1SK"] = &types.AttributeValueMemberS{Value: record.GSI1SK}
	result["operatorID"] = &types.AttributeValueMemberS{Value: record.OperatorID}
	result["groupNamespace"] = &types.AttributeValueMemberS{Value: record.GroupNamespace}
	result["groupName"] = &types.AttributeValueMemberS{Value: record.GroupName}
	result["clusterName"] = &types.AttributeValueMemberS{Value: record.ClusterName}
	result["health"] = &types.AttributeValueMemberS{Value: record.Health}
	result["state"] = &types.AttributeValueMemberS{Value: record.State}
	result["lastHeartbeat"] = &types.AttributeValueMemberS{Value: record.LastHeartbeat.Format(time.RFC3339)}
	result["components"] = &types.AttributeValueMemberS{Value: record.Components}

	return result, nil
}
