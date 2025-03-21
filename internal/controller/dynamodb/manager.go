package dynamodb

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

// DynamoDBClient defines the interface for interacting with DynamoDB
type DynamoDBClient interface {
	GetItem(ctx context.Context, params *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	PutItem(ctx context.Context, params *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	UpdateItem(ctx context.Context, params *dynamodb.UpdateItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
	DeleteItem(ctx context.Context, params *dynamodb.DeleteItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error)
	Query(ctx context.Context, params *dynamodb.QueryInput, optFns ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
	Scan(ctx context.Context, params *dynamodb.ScanInput, optFns ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error)
}

// Manager provides core functionality for interacting with DynamoDB
type Manager struct {
	client      DynamoDBClient
	tableName   string
	clusterName string
	operatorID  string
}

// GroupState represents the state of a failover group
type GroupState struct {
	GroupID        string     `json:"groupId"`
	Status         string     `json:"status"`
	CurrentRole    string     `json:"currentRole"`
	FailoverCount  int        `json:"failoverCount"`
	LastFailover   *time.Time `json:"lastFailover,omitempty"`
	LastHeartbeat  *time.Time `json:"lastHeartbeat,omitempty"`
	FailoverReason string     `json:"failoverReason,omitempty"`
	LastUpdate     int64      `json:"lastUpdate"`
}

// NewManager creates a new DynamoDB manager
func NewManager(client DynamoDBClient, tableName, clusterName, operatorID string) *Manager {
	return &Manager{
		client:      client,
		tableName:   tableName,
		clusterName: clusterName,
		operatorID:  operatorID,
	}
}

// getItemKey creates a composite key for DynamoDB items
func (m *Manager) getItemKey(itemType string, id string) string {
	return fmt.Sprintf("%s#%s#%s", m.clusterName, itemType, id)
}

// Key Workflow Scenarios

// 1. Normal Operation Workflow:
// - Each cluster periodically calls UpdateHeartbeat to report its health
// - Each cluster periodically calls SyncFailoverGroupStatus to update its view of the global state
// - If a cluster's state changes, it calls UpdateFailoverGroupStatus to report the change

// 2. Planned Failover Workflow:
// - Cluster initiating failover calls AcquireLock to obtain exclusive access
// - Initiator verifies preconditions (replication status, component health, etc.)
// - If preconditions met, calls ExecuteFailover to change ownership
// - All clusters detect the change during their next SyncFailoverGroupStatus
// - PRIMARY cluster transitions to STANDBY, STANDBY cluster transitions to PRIMARY
// - Lock is automatically released as part of ExecuteFailover transaction

// 3. Emergency Failover Workflow:
// - Similar to planned failover but skips some precondition checks
// - Uses forceFastMode to prioritize availability over perfect consistency
// - May be triggered automatically by DetectStaleHeartbeats or health monitoring

// 4. Automatic Failover Workflow:
// - Controller periodically calls DetectStaleHeartbeats
// - If PRIMARY cluster's heartbeat is stale, controller creates a Failover resource
// - Failover controller processes the resource using the emergency workflow
// - All clusters adapt to the new ownership during regular synchronization

// 5. Suspension Workflow:
// - Administrator calls UpdateSuspension to disable automatic failovers
// - Controllers check IsSuspended before creating automatic Failover resources
// - Manual Failover resources can still override suspension if force=true
