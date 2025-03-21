package dynamodb

import (
	"time"
)

// RecordType defines the types of records that can be stored in DynamoDB
type RecordType string

const (
	// RecordTypeOwnership represents a record that tracks which cluster owns a FailoverGroup
	RecordTypeOwnership RecordType = "OWNERSHIP"

	// RecordTypeHeartbeat represents a record that tracks heartbeats from operator instances
	RecordTypeHeartbeat RecordType = "HEARTBEAT"

	// RecordTypeLock represents a record used for distributed locking during failover operations
	RecordTypeLock RecordType = "LOCK"

	// RecordTypeConfig represents a record that stores configuration for a FailoverGroup
	RecordTypeConfig RecordType = "CONFIG"

	// RecordTypeFailoverHistory represents a record that stores the history of failover events
	RecordTypeFailoverHistory RecordType = "FAILOVER_HISTORY"
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

// OwnershipData represents the data structure for tracking ownership of a FailoverGroup
type OwnershipData struct {
	// GroupID is the unique identifier for the FailoverGroup
	GroupID string

	// CurrentOwner is the name of the cluster that currently owns the FailoverGroup
	CurrentOwner string

	// PreviousOwner is the name of the cluster that previously owned the FailoverGroup
	PreviousOwner string

	// LastUpdated is the timestamp when the record was last updated
	LastUpdated time.Time
}

// HeartbeatData represents the data structure for operator heartbeats
type HeartbeatData struct {
	// ClusterName is the name of the cluster sending the heartbeat
	ClusterName string

	// OperatorID is the unique identifier for the operator instance
	OperatorID string

	// LastUpdated is the timestamp when the record was last updated
	LastUpdated time.Time
}

// LockRecord represents a distributed lock for a FailoverGroup
// Used to ensure only one cluster can perform operations on a FailoverGroup at a time
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

// ConfigRecord represents configuration settings for a FailoverGroup
// Stores timeout settings, heartbeat intervals, and other configuration options
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

// FailoverHistoryRecord represents a record of a failover operation
// Tracks the details of each failover for auditing and troubleshooting
type FailoverHistoryRecord struct {
	GroupKey       string           `json:"groupKey"`          // Composite key: {operatorID}:{namespace}:{name}
	RecordType     string           `json:"recordType"`        // "FAILOVER_HISTORY:{timestamp}"
	OperatorID     string           `json:"operatorID"`        // ID of the operator instance
	GroupNamespace string           `json:"groupNamespace"`    // Kubernetes namespace of the FailoverGroup
	GroupName      string           `json:"groupName"`         // Name of the FailoverGroup
	FailoverName   string           `json:"failoverName"`      // Name of the Failover resource
	SourceCluster  string           `json:"sourceCluster"`     // Cluster that was PRIMARY before
	TargetCluster  string           `json:"targetCluster"`     // Cluster that became PRIMARY
	StartTime      time.Time        `json:"startTime"`         // When the failover started
	EndTime        time.Time        `json:"endTime"`           // When the failover completed
	Status         string           `json:"status"`            // SUCCESS, FAILED, etc.
	Reason         string           `json:"reason"`            // Why the failover was performed
	Metrics        *FailoverMetrics `json:"metrics,omitempty"` // Performance metrics for the operation
}

// ComponentStatus represents the health status of a component
// Used in HeartbeatRecord to track individual component health
type ComponentStatus struct {
	Name   string `json:"name"`   // Name of the component (database, cache, web, etc.)
	Health string `json:"health"` // Health status: OK, DEGRADED, ERROR
}

// FailoverReference is a reference to a Failover resource
// Used in OwnershipRecord to reference the last failover operation
type FailoverReference struct {
	Name      string    `json:"name"`      // Name of the Failover resource
	Namespace string    `json:"namespace"` // Namespace of the Failover resource
	Timestamp time.Time `json:"timestamp"` // When the failover occurred
}

// FailoverMetrics contains performance metrics for failover operations
// Used for monitoring and optimization
type FailoverMetrics struct {
	TotalDowntimeSeconds     int64 `json:"totalDowntimeSeconds"`     // Total application downtime
	TotalFailoverTimeSeconds int64 `json:"totalFailoverTimeSeconds"` // Total operation time
}

// TimeoutSettings defines various timeout settings for a FailoverGroup
// Used to control automatic failover behavior
type TimeoutSettings struct {
	TransitoryState  string `json:"transitoryState"`  // Max time in FAILOVER/FAILBACK states
	UnhealthyPrimary string `json:"unhealthyPrimary"` // Time PRIMARY can be unhealthy
	Heartbeat        string `json:"heartbeat"`        // Time without heartbeats before auto-failover
}
