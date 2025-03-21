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
	TransactWriteItems(ctx context.Context, params *dynamodb.TransactWriteItemsInput, optFns ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error)
}

// BaseManager provides core functionality for interacting with DynamoDB
type BaseManager struct {
	client      DynamoDBClient
	tableName   string
	clusterName string
	operatorID  string
}

// GroupState represents the state of a failover group
type GroupState struct {
	GroupID                   string     `json:"groupId"`
	Status                    string     `json:"status"`
	CurrentRole               string     `json:"currentRole"`
	FailoverCount             int        `json:"failoverCount"`
	LastFailover              *time.Time `json:"lastFailover,omitempty"`
	LastHeartbeat             *time.Time `json:"lastHeartbeat,omitempty"`
	FailoverReason            string     `json:"failoverReason,omitempty"`
	LastUpdate                int64      `json:"lastUpdate"`
	VolumeState               string     `json:"volumeState,omitempty"`               // Current state of volumes during failover
	LastVolumeStateUpdateTime string     `json:"lastVolumeStateUpdateTime,omitempty"` // When volume state was last updated
}

// DynamoDBService provides access to all DynamoDB functionality
// This is the main entry point for controllers interacting with DynamoDB
type DynamoDBService struct {
	State       *StateManager
	Operations  *OperationsManager
	ClusterName string
	OperatorID  string
	TableName   string
}

// NewDynamoDBService creates a new DynamoDB service with all required managers
func NewDynamoDBService(client DynamoDBClient, tableName, clusterName, operatorID string) *DynamoDBService {
	baseManager := NewBaseManager(client, tableName, clusterName, operatorID)
	stateManager := NewStateManager(baseManager)
	operationsManager := NewOperationsManager(baseManager)

	return &DynamoDBService{
		State:       stateManager,
		Operations:  operationsManager,
		ClusterName: clusterName,
		OperatorID:  operatorID,
		TableName:   tableName,
	}
}

// NewBaseManager creates a new DynamoDB manager
func NewBaseManager(client DynamoDBClient, tableName, clusterName, operatorID string) *BaseManager {
	return &BaseManager{
		client:      client,
		tableName:   tableName,
		clusterName: clusterName,
		operatorID:  operatorID,
	}
}

// getGroupPK creates a primary key for a FailoverGroup
func (m *BaseManager) getGroupPK(namespace, name string) string {
	return fmt.Sprintf("GROUP#%s#%s#%s", m.operatorID, namespace, name)
}

// getClusterSK creates a sort key for a cluster status record
func (m *BaseManager) getClusterSK(clusterName string) string {
	return fmt.Sprintf("CLUSTER#%s", clusterName)
}

// getHistorySK creates a sort key for a history record
func (m *BaseManager) getHistorySK(timestamp time.Time) string {
	return fmt.Sprintf("HISTORY#%s", timestamp.Format(time.RFC3339))
}

// getGSI1PK creates a GSI1 primary key for an operator
func (m *BaseManager) getOperatorGSI1PK() string {
	return fmt.Sprintf("OPERATOR#%s", m.operatorID)
}

// getGSI1SK creates a GSI1 sort key for a group
func (m *BaseManager) getGroupGSI1SK(namespace, name string) string {
	return fmt.Sprintf("GROUP#%s#%s", namespace, name)
}

// getClusterGSI1PK creates a GSI1 primary key for a cluster
func (m *BaseManager) getClusterGSI1PK(clusterName string) string {
	return fmt.Sprintf("CLUSTER#%s", clusterName)
}

// Key Workflow Scenarios

// 1. Normal Operation Workflow:
// - Each cluster periodically calls UpdateClusterStatus to report its health
// - Each cluster periodically calls GetCurrentGroupConfig to update its view of the global state
// - If a cluster's state changes, it calls UpdateClusterStatus to report the change

// 2. Planned Failover Workflow:
// - Cluster initiating failover calls AcquireLock to obtain exclusive access
// - Initiator verifies preconditions (replication status, component health, etc.)
// - If preconditions met, calls ExecuteFailover to change ownership
// - All clusters detect the change during their next GetCurrentGroupConfig
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
