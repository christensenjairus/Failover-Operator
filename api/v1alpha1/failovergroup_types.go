/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TimeoutSettings defines configurable timeouts for failover operations
type TimeoutSettings struct {
	// Maximum time a FailoverGroup can remain in FAILOVER/FAILBACK states
	// After this period, the operator initiates an automatic rollback
	// +optional
	TransitoryState string `json:"transitoryState,omitempty"`

	// Time that a PRIMARY cluster can remain unhealthy before auto-failover
	// The operator creates a failover after this period if health status=ERROR
	// +optional
	UnhealthyPrimary string `json:"unhealthyPrimary,omitempty"`

	// Time without heartbeats before assuming a cluster is down
	// The operator creates a failover after this period if no heartbeats are received
	// +optional
	Heartbeat string `json:"heartbeat,omitempty"`
}

// ClusterInfo contains information about a cluster in the FailoverGroup
type ClusterInfo struct {
	// Name of the cluster
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Role of the cluster (PRIMARY or STANDBY)
	// +kubebuilder:validation:Enum=PRIMARY;STANDBY
	// +optional
	Role string `json:"role,omitempty"`

	// Health status of the cluster
	// +kubebuilder:validation:Enum=OK;DEGRADED;ERROR
	// +optional
	Health string `json:"health,omitempty"`

	// LastHeartbeat is the timestamp of the last heartbeat received
	// +optional
	LastHeartbeat string `json:"lastHeartbeat,omitempty"`
}

// GlobalStateInfo contains global state information synced from DynamoDB
type GlobalStateInfo struct {
	// Which cluster is currently PRIMARY for this group
	// +optional
	ActiveCluster string `json:"activeCluster,omitempty"`

	// This cluster's name (for convenience)
	// +optional
	ThisCluster string `json:"thisCluster,omitempty"`

	// Reference to the most recent failover operation
	// +optional
	LastFailover map[string]string `json:"lastFailover,omitempty"`

	// Status of DynamoDB synchronization
	// +kubebuilder:validation:Enum=Synced;Syncing;Error
	// +optional
	DBSyncStatus string `json:"dbSyncStatus,omitempty"`

	// LastSyncTime is when the last successful sync with DynamoDB occurred
	// +optional
	LastSyncTime string `json:"lastSyncTime,omitempty"`

	// Information about all clusters participating in this FailoverGroup
	// +optional
	Clusters []ClusterInfo `json:"clusters,omitempty"`
}

// FailoverGroupSpec defines the desired state of FailoverGroup
type FailoverGroupSpec struct {
	// Identifier for the operator instance that should process this FailoverGroup
	// This allows running multiple operator instances for different applications
	// +optional
	OperatorID string `json:"operatorID,omitempty"`

	// DefaultFailoverMode determines the default failover approach for all components:
	// "safe" ensures data is fully synced before failover,
	// while "fast" allows immediate transition without waiting.
	// +kubebuilder:validation:Enum=safe;fast
	// +kubebuilder:validation:Required
	DefaultFailoverMode string `json:"defaultFailoverMode"`

	// When true, automatic failovers are disabled (manual override for maintenance)
	// The operator will not create automatic failovers even if it detects problems
	// +optional
	Suspended bool `json:"suspended,omitempty"`

	// Documentation field explaining why automatic failovers are suspended
	// Only meaningful when suspended=true
	// +optional
	SuspensionReason string `json:"suspensionReason,omitempty"`

	// Timeout settings for automatic operations
	// +optional
	Timeouts TimeoutSettings `json:"timeouts,omitempty"`

	// How often the operator updates heartbeats in DynamoDB
	// This controls the frequency of cluster health updates in the global state
	// +optional
	HeartbeatInterval string `json:"heartbeatInterval,omitempty"`

	// ParentFluxResources defines the Flux resources that need to be suspended during failover operations
	// +optional
	ParentFluxResources []ResourceRef `json:"parentFluxResources,omitempty"`

	// Components defines the application components and their associated resources
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Components []ComponentSpec `json:"components"`
}

// FailoverGroupStatus defines the observed state of FailoverGroup
type FailoverGroupStatus struct {
	// State reflects the actual failover state of the system.
	// Values: "PRIMARY", "STANDBY", "FAILOVER", "FAILBACK"
	// +kubebuilder:validation:Enum=PRIMARY;STANDBY;FAILOVER;FAILBACK
	State string `json:"state,omitempty"`

	// Health indicates the overall health of the failover group
	// Values: "OK", "DEGRADED", "ERROR"
	// +kubebuilder:validation:Enum=OK;DEGRADED;ERROR
	Health string `json:"health,omitempty"`

	// Components contains status information for each component defined in the spec
	// +optional
	Components []ComponentStatus `json:"components,omitempty"`

	// LastFailoverTime is the time when the last failover operation completed
	// +optional
	LastFailoverTime string `json:"lastFailoverTime,omitempty"`

	// GlobalState contains global state information synced from DynamoDB
	// +optional
	GlobalState GlobalStateInfo `json:"globalState,omitempty"`

	// Conditions represent the current state of failover reconciliation.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:printcolumn:name="State",type=string,JSONPath=`.status.state`
//+kubebuilder:printcolumn:name="Health",type=string,JSONPath=`.status.health`
//+kubebuilder:printcolumn:name="Active",type=string,JSONPath=`.status.globalState.activeCluster`
//+kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// FailoverGroup is the Schema for the failovergroups API
type FailoverGroup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   FailoverGroupSpec   `json:"spec,omitempty"`
	Status FailoverGroupStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// FailoverGroupList contains a list of FailoverGroup
type FailoverGroupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []FailoverGroup `json:"items"`
}

func init() {
	SchemeBuilder.Register(&FailoverGroup{}, &FailoverGroupList{})
}
