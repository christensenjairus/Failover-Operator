package dynamodb

import (
	"time"
)

// RecordType defines the types of records that can be stored in DynamoDB
type RecordType string

const (
	// RecordTypeGroupConfig represents a record that stores ownership and configuration for a FailoverGroup
	RecordTypeGroupConfig RecordType = "GROUP_CONFIG"

	// RecordTypeClusterStatus represents a record that tracks status and heartbeats from clusters
	RecordTypeClusterStatus RecordType = "CLUSTER_STATUS"

	// RecordTypeLock represents a record used for distributed locking during failover operations
	RecordTypeLock RecordType = "LOCK"

	// RecordTypeHistory represents a record that stores the history of failover events
	RecordTypeHistory RecordType = "HISTORY"
)

// TransactionType defines the types of transactions that can be executed
type TransactionType string

const (
	// FailoverTransaction represents a transaction for failing over from one cluster to another
	FailoverTransaction TransactionType = "FAILOVER"

	// FailbackTransaction represents a transaction for failing back to the original primary cluster
	FailbackTransaction TransactionType = "FAILBACK"

	// CleanupTransaction represents a transaction for cleaning up resources after failover
	CleanupTransaction TransactionType = "CLEANUP"
)

// ClusterRole defines the possible roles for a cluster in a FailoverGroup
type ClusterRole string

const (
	// PrimaryRole indicates the cluster is active and serving traffic
	PrimaryRole ClusterRole = "PRIMARY"

	// StandbyRole indicates the cluster is passive and not serving traffic
	StandbyRole ClusterRole = "STANDBY"
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

// GroupConfigRecord represents configuration and ownership settings for a FailoverGroup
// Combines the previous OwnershipRecord and ConfigRecord
type GroupConfigRecord struct {
	PK                string             `json:"pk"`                         // Primary Key: "GROUP#{operatorID}#{namespace}#{name}"
	SK                string             `json:"sk"`                         // Sort Key: "CONFIG"
	GSI1PK            string             `json:"gsi1pk"`                     // GSI Primary Key: "OPERATOR#{operatorID}"
	GSI1SK            string             `json:"gsi1sk"`                     // GSI Sort Key: "GROUP#{namespace}#{name}"
	OperatorID        string             `json:"operatorID"`                 // ID of the operator instance
	GroupNamespace    string             `json:"groupNamespace"`             // Kubernetes namespace of the FailoverGroup
	GroupName         string             `json:"groupName"`                  // Name of the FailoverGroup
	OwnerCluster      string             `json:"ownerCluster"`               // Name of the cluster that currently owns this group
	PreviousOwner     string             `json:"previousOwner"`              // Name of the cluster that previously owned this group
	Version           int                `json:"version"`                    // Used for optimistic concurrency control
	Timeouts          TimeoutSettings    `json:"timeouts"`                   // Timeout settings for automatic failovers
	HeartbeatInterval string             `json:"heartbeatInterval"`          // How often heartbeats should be updated
	LastUpdated       time.Time          `json:"lastUpdated"`                // When the config was last updated
	LastFailover      *FailoverReference `json:"lastFailover,omitempty"`     // Reference to the last failover operation
	Suspended         bool               `json:"suspended"`                  // Whether automatic failovers are suspended
	SuspensionReason  string             `json:"suspensionReason,omitempty"` // Why automatic failovers are suspended
	Metadata          map[string]string  `json:"metadata,omitempty"`         // Additional metadata for the group (includes volume state)
}

// ClusterStatusRecord represents the status and heartbeat of a cluster for a FailoverGroup
type ClusterStatusRecord struct {
	PK             string    `json:"pk"`             // Primary Key: "GROUP#{operatorID}#{namespace}#{name}"
	SK             string    `json:"sk"`             // Sort Key: "CLUSTER#{clusterName}"
	GSI1PK         string    `json:"gsi1pk"`         // GSI Primary Key: "CLUSTER#{clusterName}"
	GSI1SK         string    `json:"gsi1sk"`         // GSI Sort Key: "GROUP#{namespace}#{name}"
	OperatorID     string    `json:"operatorID"`     // ID of the operator instance
	GroupNamespace string    `json:"groupNamespace"` // Kubernetes namespace of the FailoverGroup
	GroupName      string    `json:"groupName"`      // Name of the FailoverGroup
	ClusterName    string    `json:"clusterName"`    // Name of the cluster this status is for
	Health         string    `json:"health"`         // Overall health: OK, DEGRADED, ERROR
	State          string    `json:"state"`          // State: PRIMARY, STANDBY, FAILOVER, FAILBACK
	LastHeartbeat  time.Time `json:"lastHeartbeat"`  // When the heartbeat was last updated
	Components     string    `json:"components"`     // JSON string of component status map for more efficient querying
}

// LockRecord represents a distributed lock for a FailoverGroup
type LockRecord struct {
	PK             string    `json:"pk"`             // Primary Key: "GROUP#{operatorID}#{namespace}#{name}"
	SK             string    `json:"sk"`             // Sort Key: "LOCK"
	OperatorID     string    `json:"operatorID"`     // ID of the operator instance
	GroupNamespace string    `json:"groupNamespace"` // Kubernetes namespace of the FailoverGroup
	GroupName      string    `json:"groupName"`      // Name of the FailoverGroup
	LockedBy       string    `json:"lockedBy"`       // Name of the cluster holding the lock
	LockReason     string    `json:"lockReason"`     // Why the lock was acquired
	AcquiredAt     time.Time `json:"acquiredAt"`     // When the lock was acquired
	ExpiresAt      time.Time `json:"expiresAt"`      // When the lock expires (for lease-based locking)
	LeaseToken     string    `json:"leaseToken"`     // Unique token to validate lock ownership
}

// HistoryRecord represents a record of a failover operation
type HistoryRecord struct {
	PK             string    `json:"pk"`             // Primary Key: "GROUP#{operatorID}#{namespace}#{name}"
	SK             string    `json:"sk"`             // Sort Key: "HISTORY#{timestamp}"
	OperatorID     string    `json:"operatorID"`     // ID of the operator instance
	GroupNamespace string    `json:"groupNamespace"` // Kubernetes namespace of the FailoverGroup
	GroupName      string    `json:"groupName"`      // Name of the FailoverGroup
	FailoverName   string    `json:"failoverName"`   // Name of the Failover resource
	SourceCluster  string    `json:"sourceCluster"`  // Cluster that was PRIMARY before
	TargetCluster  string    `json:"targetCluster"`  // Cluster that became PRIMARY
	StartTime      time.Time `json:"startTime"`      // When the failover started
	EndTime        time.Time `json:"endTime"`        // When the failover completed
	Status         string    `json:"status"`         // SUCCESS, FAILED, etc.
	Reason         string    `json:"reason"`         // Why the failover was performed
	Downtime       int64     `json:"downtime"`       // Total application downtime in seconds
	Duration       int64     `json:"duration"`       // Total operation time in seconds
}

// ComponentStatus represents the health status of a component
type ComponentStatus struct {
	Health  string `json:"health"`  // Health status: OK, DEGRADED, ERROR
	Message string `json:"message"` // Optional message with details about the health status
}

// ResourceStatus represents the status of a resource
type ResourceStatus struct {
	Kind   string `json:"kind"`   // Kind of the resource
	Name   string `json:"name"`   // Name of the resource
	Health string `json:"health"` // Health status: OK, DEGRADED, ERROR
	Status string `json:"status"` // Status message about the resource
}

// WorkloadReplicationStatus represents the status of volume replications for a workload
type WorkloadReplicationStatus struct {
	WorkloadKind       string           `json:"workloadKind"`       // Kind of the workload
	WorkloadName       string           `json:"workloadName"`       // Name of the workload
	VolumeReplications []ResourceStatus `json:"volumeReplications"` // Status of volume replications
}

// StatusData represents the detailed status information for all resource types
type StatusData struct {
	Workloads          []ResourceStatus            `json:"workloads,omitempty"`
	NetworkResources   []ResourceStatus            `json:"networkResources,omitempty"`
	FluxResources      []ResourceStatus            `json:"fluxResources,omitempty"`
	VolumeReplications []WorkloadReplicationStatus `json:"volumeReplications,omitempty"`
}

// FailoverReference is a reference to a Failover resource
type FailoverReference struct {
	Name      string    `json:"name"`      // Name of the Failover resource
	Namespace string    `json:"namespace"` // Namespace of the Failover resource
	Timestamp time.Time `json:"timestamp"` // When the failover occurred
}

// TimeoutSettings defines various timeout settings for a FailoverGroup
type TimeoutSettings struct {
	TransitoryState  string `json:"transitoryState"`  // Max time in FAILOVER/FAILBACK states
	UnhealthyPrimary string `json:"unhealthyPrimary"` // Time PRIMARY can be unhealthy
	Heartbeat        string `json:"heartbeat"`        // Time without heartbeats before auto-failover
}

// FailoverGroupState represents the consolidated state of a failover group
type FailoverGroupState struct {
	// ActiveCluster is the currently active cluster for this group
	ActiveCluster string `json:"activeCluster"`

	// LastFailover contains information about the most recent failover
	LastFailover *FailoverRecord `json:"lastFailover,omitempty"`

	// Clusters maps cluster names to their states
	Clusters map[string]ClusterState `json:"clusters"`

	// LastUpdate is the timestamp when this state was last updated
	LastUpdate time.Time `json:"lastUpdate"`

	// Suspended indicates whether automatic failovers are disabled
	Suspended bool `json:"suspended"`

	// SuspensionReason explains why failovers are suspended (if applicable)
	SuspensionReason string `json:"suspensionReason,omitempty"`
}

// ClusterState represents the state of a single cluster in a failover group
type ClusterState struct {
	// Role of the cluster (PRIMARY or STANDBY)
	Role string `json:"role"`

	// Health status of the cluster (OK, DEGRADED, ERROR)
	Health string `json:"health"`

	// LastHeartbeat is the last time this cluster reported its status
	LastHeartbeat time.Time `json:"lastHeartbeat"`
}

// FailoverRecord contains information about a failover operation
type FailoverRecord struct {
	// Timestamp when the failover occurred
	Timestamp time.Time `json:"timestamp"`

	// SourceCluster is the cluster that was PRIMARY before the failover
	SourceCluster string `json:"sourceCluster"`

	// TargetCluster is the cluster that became PRIMARY after the failover
	TargetCluster string `json:"targetCluster"`

	// Reason explains why the failover occurred
	Reason string `json:"reason,omitempty"`

	// Emergency indicates whether this was an emergency failover
	Emergency bool `json:"emergency"`

	// Duration is how long the failover took to complete
	DurationSeconds int64 `json:"durationSeconds"`
}

// SimpleLockRecord is a simplified version of LockRecord for external use
type SimpleLockRecord struct {
	// Owner identifies which operator instance holds the lock
	Owner string `json:"owner"`

	// Expiry is when the lock automatically expires
	Expiry time.Time `json:"expiry"`

	// Operation describes what operation the lock is for
	Operation string `json:"operation"`
}

// GroupState represents the global state of a FailoverGroup across all clusters
type GroupState struct {
	// ActiveCluster is the currently active cluster for this group
	ActiveCluster string `json:"activeCluster"`

	// ThisCluster is the name of the cluster this state is for (convenience)
	ThisCluster string `json:"thisCluster,omitempty"`

	// LastFailover contains information about the most recent failover
	LastFailover map[string]string `json:"lastFailover,omitempty"`

	// DBSyncStatus indicates the status of DynamoDB synchronization
	DBSyncStatus string `json:"dbSyncStatus,omitempty"`

	// LastSyncTime is when the last successful sync with DynamoDB occurred
	LastSyncTime string `json:"lastSyncTime,omitempty"`

	// Clusters is information about all clusters participating in this FailoverGroup
	Clusters []ClusterInfo `json:"clusters,omitempty"`

	// VolumeState tracks the state of volumes during failover
	VolumeState string `json:"volumeState,omitempty"`

	// LastVolumeStateUpdateTime is when the volume state was last updated
	LastVolumeStateUpdateTime string `json:"lastVolumeStateUpdateTime,omitempty"`
}

// ClusterInfo represents information about a specific cluster
type ClusterInfo struct {
	// Name of the cluster
	Name string `json:"name"`

	// Role of the cluster (PRIMARY or STANDBY)
	Role string `json:"role,omitempty"`

	// Health status of the cluster
	Health string `json:"health,omitempty"`

	// LastHeartbeat is the timestamp of the last heartbeat received
	LastHeartbeat string `json:"lastHeartbeat,omitempty"`
}

// With:
type StateMgrConfig struct {
	Table      string
	Client     DynamoDBClient
	OperatorID string
	Operations *OperationsManager
}
