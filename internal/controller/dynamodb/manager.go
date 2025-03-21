package dynamodb

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"sigs.k8s.io/controller-runtime/pkg/log"
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

// ManagerGroupState represents the state of a failover group
type ManagerGroupState struct {
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
	*BaseManager
	ClusterName        string
	OperatorID         string
	TableName          string
	volumeStateManager *VolumeStateManager
	operationsManager  *OperationsManager
}

// NewDynamoDBService creates a new DynamoDB service with all required managers
func NewDynamoDBService(client DynamoDBClient, tableName, clusterName, operatorID string) *DynamoDBService {
	baseManager := NewBaseManager(client, tableName, clusterName, operatorID)
	operationsManager := NewOperationsManager(baseManager)
	volumeStateManager := NewVolumeStateManager(baseManager)

	return &DynamoDBService{
		BaseManager:        baseManager,
		ClusterName:        clusterName,
		OperatorID:         operatorID,
		TableName:          tableName,
		volumeStateManager: volumeStateManager,
		operationsManager:  operationsManager,
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

// Operations methods

// ExecuteFailover executes a failover from one cluster to another
func (s *DynamoDBService) ExecuteFailover(ctx context.Context, namespace, name, failoverName, targetCluster, reason string, forceFastMode bool) error {
	return s.operationsManager.ExecuteFailover(ctx, namespace, name, failoverName, targetCluster, reason, forceFastMode)
}

// ExecuteFailback executes a failback to the original primary cluster
func (s *DynamoDBService) ExecuteFailback(ctx context.Context, namespace, name, reason string) error {
	return s.operationsManager.ExecuteFailback(ctx, namespace, name, reason)
}

// ValidateFailoverPreconditions validates that preconditions for a failover are met
func (s *DynamoDBService) ValidateFailoverPreconditions(ctx context.Context, namespace, name, targetCluster string, skipHealthCheck bool) error {
	return s.operationsManager.ValidateFailoverPreconditions(ctx, namespace, name, targetCluster, skipHealthCheck)
}

// UpdateSuspension updates the suspension status of a FailoverGroup
func (s *DynamoDBService) UpdateSuspension(ctx context.Context, namespace, name string, suspended bool, reason string) error {
	return s.operationsManager.UpdateSuspension(ctx, namespace, name, suspended, reason)
}

// AcquireLock acquires a lock for a FailoverGroup
func (s *DynamoDBService) AcquireLock(ctx context.Context, namespace, name, reason string) (string, error) {
	return s.operationsManager.AcquireLock(ctx, namespace, name, reason)
}

// ReleaseLock releases a lock for a FailoverGroup
func (s *DynamoDBService) ReleaseLock(ctx context.Context, namespace, name, leaseToken string) error {
	return s.operationsManager.ReleaseLock(ctx, namespace, name, leaseToken)
}

// IsLocked checks if a FailoverGroup is locked
func (s *DynamoDBService) IsLocked(ctx context.Context, namespace, name string) (bool, string, error) {
	return s.operationsManager.IsLocked(ctx, namespace, name)
}

// DetectAndReportProblems finds and reports problems with the FailoverGroup
func (s *DynamoDBService) DetectAndReportProblems(ctx context.Context, namespace, name string) ([]string, error) {
	return s.operationsManager.DetectAndReportProblems(ctx, namespace, name)
}

// VolumeState methods

// GetVolumeState retrieves the current volume state for a failover group
func (s *DynamoDBService) GetVolumeState(ctx context.Context, namespace, groupName string) (string, error) {
	return s.volumeStateManager.GetVolumeState(ctx, namespace, groupName)
}

// SetVolumeState updates the volume state for a failover group
func (s *DynamoDBService) SetVolumeState(ctx context.Context, namespace, groupName, state string) error {
	return s.volumeStateManager.SetVolumeState(ctx, namespace, groupName, state)
}

// UpdateHeartbeat updates the heartbeat timestamp for a cluster
func (s *DynamoDBService) UpdateHeartbeat(ctx context.Context, namespace, groupName, clusterName string) error {
	return s.volumeStateManager.UpdateHeartbeat(ctx, namespace, groupName, clusterName)
}

// RemoveVolumeState removes the volume state information
func (s *DynamoDBService) RemoveVolumeState(ctx context.Context, namespace, groupName string) error {
	return s.volumeStateManager.RemoveVolumeState(ctx, namespace, groupName)
}

// StateManager methods that aren't in BaseManager

// SyncClusterState synchronizes the cluster state with DynamoDB
func (s *DynamoDBService) SyncClusterState(ctx context.Context, namespace, name string) error {
	// This is a stub implementation that would typically synchronize the local cluster state
	// with the state stored in DynamoDB
	logger := log.FromContext(ctx).WithValues(
		"namespace", namespace,
		"name", name,
	)
	logger.V(1).Info("Synchronizing cluster state with DynamoDB")
	return nil
}

// DetectStaleHeartbeats detects clusters with stale heartbeats
func (s *DynamoDBService) DetectStaleHeartbeats(ctx context.Context, namespace, name string) ([]string, error) {
	// This is a stub implementation that would typically check for stale heartbeats
	// and return a list of clusters that have not updated their heartbeat recently
	logger := log.FromContext(ctx).WithValues(
		"namespace", namespace,
		"name", name,
	)
	logger.V(1).Info("Detecting stale heartbeats")
	return []string{}, nil
}

// GetAllClusterStatuses gets the status of all clusters in the failover group
func (s *DynamoDBService) GetAllClusterStatuses(ctx context.Context, namespace, name string) (map[string]*ClusterStatusRecord, error) {
	// This is a stub implementation that would typically query DynamoDB
	// to get the status of all clusters in the failover group
	logger := log.FromContext(ctx).WithValues(
		"namespace", namespace,
		"name", name,
	)
	logger.V(1).Info("Getting all cluster statuses")

	// Create a map with just this cluster for now
	statuses := make(map[string]*ClusterStatusRecord)
	status, err := s.GetClusterStatus(ctx, namespace, name, s.ClusterName)
	if err != nil {
		return nil, err
	}
	statuses[s.ClusterName] = status

	return statuses, nil
}

// UpdateClusterStatus updates the status of a cluster in DynamoDB
func (s *DynamoDBService) UpdateClusterStatus(ctx context.Context, namespace, name, health, state string, statusData *StatusData) error {
	// This is a stub implementation that would typically update the status of a cluster in DynamoDB
	logger := log.FromContext(ctx).WithValues(
		"namespace", namespace,
		"name", name,
		"health", health,
		"state", state,
	)
	logger.V(1).Info("Updating cluster status")

	// Forward to base manager implementation
	return s.UpdateClusterStatusLegacy(ctx, namespace, name, health, state, nil)
}
