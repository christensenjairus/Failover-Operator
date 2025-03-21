package dynamodb

import (
	"context"
	"time"

	"github.com/aws/aws-sdk-go/service/dynamodb"
	crdv1alpha1 "github.com/christensenjairus/Failover-Operator/api/v1alpha1"
)

// Record types for DynamoDB
const (
	RecordTypeOwnership       = "OWNERSHIP"       // Stores which cluster is PRIMARY for a FailoverGroup
	RecordTypeHeartbeatPrefix = "HEARTBEAT:"      // Prefix for heartbeat records, followed by cluster name
	RecordTypeLock            = "LOCK"            // Used for distributed locking during failover operations
	RecordTypeConfig          = "CONFIG"          // Stores configuration information shared across clusters
	RecordTypeFailoverHistory = "FAILOVER_HISTORY:" // Prefix for failover history records, followed by timestamp
)

// Health status constants
const (
	HealthOK       = "OK"       // All components are healthy
	HealthDegraded = "DEGRADED" // Some components have issues but are functioning
	HealthError    = "ERROR"    // Critical components are not functioning properly
)

// State constants
const (
	StatePrimary  = "PRIMARY"  // Cluster is actively serving the application
	StateStandby  = "STANDBY"  // Cluster is passive (standby) for the application
	StateFailover = "FAILOVER" // Transitioning from PRIMARY to STANDBY
	StateFailback = "FAILBACK" // Transitioning from STANDBY to PRIMARY
)

// OwnershipRecord represents the ownership information for a FailoverGroup
// This is the core record that determines which cluster is PRIMARY for a FailoverGroup
type OwnershipRecord struct {
	GroupKey         string             `json:"groupKey"`         // Composite key: {operatorID}:{namespace}:{name}
	RecordType       string             `json:"recordType"`       // Always "OWNERSHIP"
	OperatorID       string             `json:"operatorID"`       // ID of the operator instance managing this group
	GroupNamespace   string             `json:"groupNamespace"`   // Kubernetes namespace of the FailoverGroup
	GroupName        string             `json:"groupName"`        // Name of the FailoverGroup
	OwnerCluster     string             `json:"ownerCluster"`     // Name of the cluster that is PRIMARY
	Version          int                `json:"version"`          // Used for optimistic concurrency control
	LastUpdated      time.Time          `json:"lastUpdated"`      // When the ownership was last updated
	LastFailover     *FailoverReference `json:"lastFailover,omitempty"` // Reference to the most recent failover
	Suspended        bool               `json:"suspended"`        // Whether automatic failovers are disabled
	SuspensionReason string             `json:"suspensionReason,omitempty"` // Why failovers are suspended
	SuspendedBy      string             `json:"suspendedBy,omitempty"`      // Which user/system suspended
	SuspendedAt      string             `json:"suspendedAt,omitempty"`      // When it was suspended
}

// HeartbeatRecord represents the heartbeat information for a cluster
// Each cluster periodically updates its heartbeat to indicate it's alive and healthy
type HeartbeatRecord struct {
	GroupKey       string            `json:"groupKey"`       // Composite key: {operatorID}:{namespace}:{name}
	RecordType     string            `json:"recordType"`     // "HEARTBEAT:{clusterName}"
	OperatorID     string            `json:"operatorID"`     // ID of the operator instance
	GroupNamespace string            `json:"groupNamespace"` // Kubernetes namespace of the FailoverGroup
	GroupName      string            `json:"groupName"`      // Name of the FailoverGroup
	ClusterName    string            `json:"clusterName"`    // Name of the cluster
	Health         string            `json:"health"`         // Overall health: OK, DEGRADED, ERROR
	State          string            `json:"state"`          // Current state: PRIMARY, STANDBY, etc.
	Timestamp      time.Time         `json:"timestamp"`      // When heartbeat was last updated
	Components     []ComponentStatus `json:"components"`     // Health status of individual components
}

// LockRecord represents a distributed lock for a FailoverGroup
// Used to ensure only one cluster can modify ownership at a time
type LockRecord struct {
	GroupKey       string    `json:"groupKey"`       // Composite key: {operatorID}:{namespace}:{name}
	RecordType     string    `json:"recordType"`     // Always "LOCK"
	OperatorID     string    `json:"operatorID"`     // ID of the operator instance
	GroupNamespace string    `json:"groupNamespace"` // Kubernetes namespace of the FailoverGroup
	GroupName      string    `json:"groupName"`      // Name of the FailoverGroup
	LockedBy       string    `json:"lockedBy"`       // Name of the cluster holding the lock
	LockReason     string    `json:"lockReason"`     // Why the lock was acquired
	AcquiredAt     time.Time `json:"acquiredAt"`     // When the lock was acquired
	ExpiresAt      time.Time `json:"expiresAt"`      // When the lock expires (for lease-based locking)
	LeaseToken     string    `json:"leaseToken"`     // Unique token to validate lock ownership
}

// ConfigRecord represents configuration information for a FailoverGroup
// Stores settings that should be consistent across all clusters
type ConfigRecord struct {
	GroupKey          string          `json:"groupKey"`          // Composite key: {operatorID}:{namespace}:{name}
	RecordType        string          `json:"recordType"`        // Always "CONFIG"
	OperatorID        string          `json:"operatorID"`        // ID of the operator instance
	GroupNamespace    string          `json:"groupNamespace"`    // Kubernetes namespace of the FailoverGroup
	GroupName         string          `json:"groupName"`         // Name of the FailoverGroup
	Version           int             `json:"version"`           // Used for optimistic concurrency control
	Timeouts          TimeoutSettings `json:"timeouts"`          // Timeout settings for automatic failovers
	HeartbeatInterval string          `json:"heartbeatInterval"` // How often heartbeats should be updated
	LastUpdated       time.Time       `json:"lastUpdated"`       // When the config was last updated
}

// FailoverHistoryRecord represents a historical record of a failover operation
// These records provide an audit trail of all failover operations
type FailoverHistoryRecord struct {
	GroupKey       string           `json:"groupKey"`       // Composite key: {operatorID}:{namespace}:{name}
	RecordType     string           `json:"recordType"`     // "FAILOVER_HISTORY:{timestamp}"
	OperatorID     string           `json:"operatorID"`     // ID of the operator instance
	GroupNamespace string           `json:"groupNamespace"` // Kubernetes namespace of the FailoverGroup
	GroupName      string           `json:"groupName"`      // Name of the FailoverGroup
	FailoverName   string           `json:"failoverName"`   // Name of the Failover resource
	SourceCluster  string           `json:"sourceCluster"`  // Cluster that was PRIMARY before
	TargetCluster  string           `json:"targetCluster"`  // Cluster that became PRIMARY
	StartTime      time.Time        `json:"startTime"`      // When the failover started
	EndTime        time.Time        `json:"endTime"`        // When the failover completed
	Status         string           `json:"status"`         // SUCCESS, FAILED, etc.
	Reason         string           `json:"reason"`         // Why the failover was performed
	Metrics        *FailoverMetrics `json:"metrics,omitempty"` // Performance metrics for the operation
}

// ComponentStatus represents the health status of a component
type ComponentStatus struct {
	Name   string `json:"name"`   // Name of the component (database, cache, web, etc.)
	Health string `json:"health"` // Health status: OK, DEGRADED, ERROR
}

// FailoverReference contains information about a failover operation
type FailoverReference struct {
	Name      string    `json:"name"`      // Name of the Failover resource
	Namespace string    `json:"namespace"` // Namespace of the Failover resource
	Timestamp time.Time `json:"timestamp"` // When the failover occurred
}

// FailoverMetrics contains performance metrics for a failover operation
type FailoverMetrics struct {
	TotalDowntimeSeconds     int64 `json:"totalDowntimeSeconds"`     // Total application downtime
	TotalFailoverTimeSeconds int64 `json:"totalFailoverTimeSeconds"` // Total operation time
}

// TimeoutSettings defines configurable timeouts for failover operations
type TimeoutSettings struct {
	TransitoryState  string `json:"transitoryState"`  // Max time in FAILOVER/FAILBACK states
	UnhealthyPrimary string `json:"unhealthyPrimary"` // Time PRIMARY can be unhealthy
	Heartbeat        string `json:"heartbeat"`        // Time without heartbeats before auto-failover
}

// Manager handles DynamoDB interactions for the failover system
// This is the main interface between the controllers and DynamoDB
type Manager struct {
	dynamoClient *dynamodb.DynamoDB
	tableName    string
	operatorID   string
	clusterName  string
}

// NewManager creates a new manager instance
func NewManager(dynamoClient *dynamodb.DynamoDB, tableName, operatorID, clusterName string) *Manager {
	return &Manager{
		dynamoClient: dynamoClient,
		tableName:    tableName,
		operatorID:   operatorID,
		clusterName:  clusterName,
	}
}

// buildGroupKey creates a standardized group key for a FailoverGroup
// This ensures consistency in key formation across all operations
func (m *Manager) buildGroupKey(namespace, name string) string {
	return m.operatorID + ":" + namespace + ":" + name
}

// GetOwnership retrieves the current ownership information for a FailoverGroup
// This is a critical function that determines which cluster should be PRIMARY
func (m *Manager) GetOwnership(ctx context.Context, namespace, name string) (*OwnershipRecord, error) {
	// This function should:
	// 1. Create a DynamoDB GetItem request using the buildGroupKey function and OWNERSHIP record type
	// 2. Execute the request to fetch the ownership record
	// 3. Unmarshal the response into an OwnershipRecord struct
	// 4. Return the ownership information or an error if not found
	//
	// If no record exists, this likely means this is a new FailoverGroup that hasn't been
	// registered in DynamoDB yet. In that case, the calling code should initialize it.

	return nil, nil
}

// UpdateOwnership updates the ownership information for a FailoverGroup
// Uses optimistic concurrency control to prevent conflicts
func (m *Manager) UpdateOwnership(ctx context.Context, namespace, name, clusterName string,
	version int, failoverRef *FailoverReference) error {
	// This function should:
	// 1. Create a DynamoDB UpdateItem request with optimistic concurrency control
	// 2. Set the new owner cluster, increment version, update lastUpdated timestamp
	// 3. If failoverRef is provided, update the lastFailover information
	// 4. Use a condition expression that ensures the current version matches the expected version
	// 5. If the condition fails, it means another cluster modified the record (conflict)
	//
	// Optimistic concurrency control is crucial here to prevent split-brain scenarios
	// where multiple clusters think they're PRIMARY simultaneously.

	return nil
}

// AcquireLock attempts to acquire a distributed lock for a failover operation
// Returns a lease token if successful, which must be used for subsequent lock operations
func (m *Manager) AcquireLock(ctx context.Context, namespace, name, reason string, expiryDuration time.Duration) (string, error) {
	// This function should:
	// 1. Generate a unique lease token (UUID)
	// 2. Create a DynamoDB PutItem request for the LOCK record
	// 3. Set the locked by, reason, acquired time, and expiry time
	// 4. Use a condition expression to only succeed if:
	//    - lock doesn't exist, OR
	//    - existing lock has expired
	// 5. Return the lease token if successful, or an error if the lock is already held
	//
	// This lease-based locking is essential for preventing deadlocks if a cluster
	// crashes while holding a lock. The expiry time ensures locks eventually release.

	return "", nil
}

// ReleaseLock releases a previously acquired lock
// Only succeeds if the lock is still held by this cluster and the lease token matches
func (m *Manager) ReleaseLock(ctx context.Context, namespace, name, leaseToken string) error {
	// This function should:
	// 1. Create a DynamoDB DeleteItem request for the LOCK record
	// 2. Use a condition expression to ensure the lock is only deleted if:
	//    - the lockedBy matches this cluster, AND
	//    - the leaseToken matches the provided token
	// 3. Handle errors, especially if the lock is held by another cluster
	//
	// The lease token validation ensures that only the cluster that originally
	// acquired the lock can release it, even if the cluster names match.

	return nil
}

// RenewLock extends the expiry time of a previously acquired lock
// Used during long-running operations to prevent lock expiration
func (m *Manager) RenewLock(ctx context.Context, namespace, name, leaseToken string, expiryDuration time.Duration) error {
	// This function should:
	// 1. Create a DynamoDB UpdateItem request for the LOCK record
	// 2. Update only the expiresAt field
	// 3. Use a condition expression to ensure the lock is only updated if:
	//    - the lockedBy matches this cluster, AND
	//    - the leaseToken matches the provided token
	// 4. Handle errors, especially if the lock is no longer valid
	//
	// Lock renewal is essential for long-running failover operations that
	// might take longer than the initial expiry duration.

	return nil
}

// UpdateHeartbeat updates the heartbeat information for this cluster
// This is how clusters communicate their health and state to each other
func (m *Manager) UpdateHeartbeat(ctx context.Context, namespace, name, health, state string,
	components []ComponentStatus) error {
	// This function should:
	// 1. Create a DynamoDB PutItem request for the HEARTBEAT record
	// 2. Set the current time, health status, state, and component statuses
	// 3. No conditional expression needed - always update the heartbeat
	// 4. Handle any errors that occur during the operation
	//
	// Heartbeats should be updated frequently (as specified by heartbeatInterval)
	// to provide an up-to-date view of cluster health across the system.

	return nil
}

// GetAllHeartbeats retrieves heartbeats for all clusters for a FailoverGroup
// Used to build a complete picture of all clusters in the system
func (m *Manager) GetAllHeartbeats(ctx context.Context, namespace, name string) (map[string]*HeartbeatRecord, error) {
	// This function should:
	// 1. Create a DynamoDB Query request with a key condition expression:
	//    - groupKey equals the FailoverGroup's key
	//    - recordType begins with "HEARTBEAT:"
	// 2. Execute the query to get all heartbeat records
	// 3. Unmarshal the results into a map of cluster name to HeartbeatRecord
	// 4. Return the map of heartbeats or an error if the query fails
	//
	// This function is crucial for displaying the global state in the
	// FailoverGroup status and for detecting unhealthy/stale clusters.

	return nil, nil
}

// DetectStaleHeartbeats checks for clusters with stale heartbeats
// Used to trigger automatic failovers when a cluster stops responding
func (m *Manager) DetectStaleHeartbeats(ctx context.Context, namespace, name string, heartbeatTimeout time.Duration) ([]string, error) {
	// This function should:
	// 1. Get all heartbeats using GetAllHeartbeats
	// 2. Check each heartbeat's timestamp against the current time
	// 3. If a heartbeat is older than the timeout, consider it stale
	// 4. Return a list of cluster names with stale heartbeats
	//
	// This is one of the key mechanisms for automatic failover detection.
	// If the PRIMARY cluster's heartbeat is stale, a failover should be triggered.

	return nil, nil
}

// RecordFailoverHistory adds a historical record of a failover operation
// Creates an audit trail of all failover operations for review and analysis
func (m *Manager) RecordFailoverHistory(ctx context.Context, namespace, name, failoverName string,
	sourceCluster, targetCluster string, status, reason string,
	metrics *FailoverMetrics) error {
	// This function should:
	// 1. Create a DynamoDB PutItem request for the FAILOVER_HISTORY record
	// 2. Use the current timestamp in the record type to make it unique
	// 3. Store all the failover details including source/target clusters and metrics
	// 4. No conditional expression needed - we want to always add history
	// 5. Handle any errors that occur during the operation
	//
	// These history records provide valuable insights for troubleshooting
	// and understanding patterns of failures or planned migrations.

	return nil
}

// GetFailoverHistory retrieves historical failover records for a FailoverGroup
// Used for audit, reporting, and analytics purposes
func (m *Manager) GetFailoverHistory(ctx context.Context, namespace, name string, limit int) ([]FailoverHistoryRecord, error) {
	// This function should:
	// 1. Create a DynamoDB Query request with a key condition expression:
	//    - groupKey equals the FailoverGroup's key
	//    - recordType begins with "FAILOVER_HISTORY:"
	// 2. Set a limit on the number of records to return
	// 3. Sort by sort key in descending order to get most recent first
	// 4. Unmarshal the results into FailoverHistoryRecord structs
	// 5. Return the history records or an error if the query fails
	//
	// This history provides a timeline of failover events, useful for
	// pattern analysis, auditing, and measuring MTTR improvements.

	return nil, nil
}

// UpdateConfig updates the configuration information for a FailoverGroup
// Ensures all clusters use the same timeout and heartbeat settings
func (m *Manager) UpdateConfig(ctx context.Context, namespace, name string,
	timeouts TimeoutSettings, heartbeatInterval string, version int) error {
	// This function should:
	// 1. Create a DynamoDB UpdateItem request for the CONFIG record
	// 2. Update the timeouts, heartbeat interval, version, and lastUpdated time
	// 3. Use optimistic concurrency control to ensure version consistency
	// 4. Handle version conflicts and other errors
	//
	// Centralizing configuration in DynamoDB ensures all clusters use
	// consistent timeout and heartbeat settings.

	return nil
}

// GetConfig retrieves the configuration information for a FailoverGroup
// Used to synchronize configuration across all clusters
func (m *Manager) GetConfig(ctx context.Context, namespace, name string) (*ConfigRecord, error) {
	// This function should:
	// 1. Create a DynamoDB GetItem request for the CONFIG record
	// 2. Execute the request to fetch the configuration
	// 3. Unmarshal the response into a ConfigRecord struct
	// 4. Return the configuration or an error if not found
	//
	// If not found, the calling code should initialize the configuration
	// with default values from the FailoverGroup spec.

	return nil, nil
}

// IsSuspended checks if a FailoverGroup is suspended
// Used to prevent automatic failovers during maintenance
func (m *Manager) IsSuspended(ctx context.Context, namespace, name string) (bool, string, error) {
	// This function should:
	// 1. Call GetOwnership to retrieve the ownership record
	// 2. Check the suspended field to determine if suspended
	// 3. Return the suspension status and reason, or an error if retrieval fails
	//
	// Suspension is an important safety mechanism to prevent unwanted
	// automatic failovers during maintenance or other planned activities.

	return false, "", nil
}

// UpdateSuspension sets or clears the suspension status of a FailoverGroup
// Used to enable/disable automatic failovers
func (m *Manager) UpdateSuspension(ctx context.Context, namespace, name string,
	suspended bool, reason string) error {
	// This function should:
	// 1. Create a DynamoDB UpdateItem request for the OWNERSHIP record
	// 2. Update the suspended field, suspensionReason, suspendedBy, and suspendedAt
	// 3. If suspended is false, clear these fields
	// 4. Handle any errors that occur during the operation
	//
	// This provides an administrative override to prevent automatic
	// failovers when they would be disruptive or dangerous.

	return nil
}

// RunTransaction executes multiple DynamoDB operations as a single transaction
// Used for operations that require atomicity across multiple records
func (m *Manager) RunTransaction(ctx context.Context, transactItems []*dynamodb.TransactWriteItem) error {
	// This function should:
	// 1. Create a DynamoDB TransactWriteItems request with the provided items
	// 2. Execute the transaction request
	// 3. Handle transaction conflicts and other errors
	// 4. Return an error if the transaction fails
	//
	// Transactions are essential for operations that require atomic updates
	// to multiple records, such as updating ownership and recording history
	// in a single operation.

	return nil
}

// BuildClusterStatusMap converts heartbeat records to a map for status updates
// Used to populate the globalState.clusters field in FailoverGroup status
func (m *Manager) BuildClusterStatusMap(heartbeats map[string]*HeartbeatRecord) []crdv1alpha1.ClusterInfo {
	// This function should:
	// 1. Create a slice of ClusterInfo structs
	// 2. For each heartbeat, create a ClusterInfo with:
	//    - Name from the cluster name
	//    - Role from the heartbeat state
	//    - Health from the heartbeat health
	//    - LastHeartbeat from the formatted timestamp
	// 3. Return the slice of ClusterInfo structs
	//
	// This provides a concise view of all clusters in the system
	// for display in the FailoverGroup status.

	return nil
}

// UpdateFailoverGroupStatus updates the DynamoDB records based on a FailoverGroup status
// Synchronizes the local state to DynamoDB
func (m *Manager) UpdateFailoverGroupStatus(ctx context.Context, failoverGroup *crdv1alpha1.FailoverGroup) error {
	// This function should:
	// 1. Extract relevant status information from the FailoverGroup
	// 2. Update the heartbeat record for this cluster
	// 3. If this cluster is the owner, consider updating the ownership record
	// 4. Handle any errors that occur during the operations
	//
	// This is primarily used for heartbeat updates, not for ownership changes
	// which should go through the proper failover process.

	return nil
}

// SyncFailoverGroupStatus syncs the FailoverGroup status with DynamoDB
// Synchronizes the DynamoDB state to the local FailoverGroup status
func (m *Manager) SyncFailoverGroupStatus(ctx context.Context, failoverGroup *crdv1alpha1.FailoverGroup) error {
	// This function should:
	// 1. Get the current ownership record
	// 2. Get all heartbeats for all clusters
	// 3. Update the FailoverGroup's GlobalState field with information from DynamoDB:
	//    - activeCluster: which cluster is PRIMARY
	//    - thisCluster: this cluster's name (convenience)
	//    - lastFailover: reference to the most recent failover
	//    - clusters: list of all known clusters with their health and state
	// 4. Set the dbSyncStatus and lastSyncTime
	// 5. Handle any errors during the synchronization
	//
	// This is how clusters learn about the global state and adapt their
	// local behavior accordingly.

	return nil
}

// ExecuteFailover performs all DynamoDB operations needed for a failover
// This is the core function that changes ownership in DynamoDB
func (m *Manager) ExecuteFailover(ctx context.Context, namespace, name, failoverName string,
	sourceCluster, targetCluster string, metrics *FailoverMetrics) error {
	// This function should:
	// 1. Prepare a transaction with multiple operations:
	//    - Update the ownership record to set the new owner cluster
	//    - Add a failover history record
	//    - Release any lock if present
	// 2. Run the transaction to ensure atomicity
	// 3. Handle transaction failures and retry if necessary
	//
	// This is the culmination of the failover process, where the actual
	// ownership change occurs. Using a transaction ensures all related
	// records are updated atomically.

	return nil
}

// InitializeTable creates the DynamoDB table and any required indexes
// Used during operator startup to ensure the table exists
func (m *Manager) InitializeTable(ctx context.Context) error {
	// This function should:
	// 1. Check if the table already exists
	// 2. If not, create the table with the correct schema:
	//    - Primary key: groupKey (String)
	//    - Sort key: recordType (String)
	// 3. Create the OperatorIndex GSI:
	//    - Partition key: operatorID (String)
	//    - Sort key: recordType (String)
	// 4. Wait for the table to become active
	//
	// This is typically called during operator startup to ensure
	// the DynamoDB table is properly configured.

	return nil
}

// DeleteTable deletes the DynamoDB table (for testing/cleanup)
// Should only be used in test environments
func (m *Manager) DeleteTable(ctx context.Context) error {
	// This function should:
	// 1. Create a DeleteTable request
	// 2. Execute the request
	// 3. Wait for the table to be deleted
	//
	// This is primarily for testing purposes and should not be
	// used in production environments.

	return nil
}

// ScanAllGroups retrieves all FailoverGroups for this operator
// Used during operator startup to discover all groups to reconcile
func (m *Manager) ScanAllGroups(ctx context.Context) ([]OwnershipRecord, error) {
	// This function should:
	// 1. Create a DynamoDB Query request using the OperatorIndex GSI
	// 2. Query for records with:
	//    - operatorID equal to this operator's ID
	//    - recordType equal to OWNERSHIP
	// 3. Unmarshal the results into OwnershipRecord structs
	// 4. Return the ownership records or an error if the query fails
	//
	// This is useful for operator initialization to discover all
	// FailoverGroups that should be reconciled.

	return nil, nil
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