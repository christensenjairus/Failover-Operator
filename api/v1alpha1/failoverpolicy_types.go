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

// FailoverPolicySpec defines the desired state of FailoverPolicy
type FailoverPolicySpec struct {
	// DesiredState represents the intended failover state ("primary" or "secondary").
	// +kubebuilder:validation:Enum=primary;secondary
	DesiredState string `json:"desiredState"`

	// Mode determines the failover approach. "safe" ensures VolumeReplication is fully synced before failover,
	// while "unsafe" allows immediate transition without waiting.
	// +kubebuilder:validation:Enum=safe;unsafe
	Mode string `json:"mode"`

	// VolumeReplications is a list of VolumeReplication objects to manage in this failover policy.
	// +kubebuilder:validation:MinItems=1
	VolumeReplications []string `json:"volumeReplications"`

	// VirtualServices is a list of VirtualService objects to update during failover.
	// +kubebuilder:validation:MinItems=1
	VirtualServices []string `json:"virtualServices"`

	// Deployments is a list of Deployment objects to scale down to 0 replicas when in secondary mode.
	// +optional
	Deployments []string `json:"deployments,omitempty"`

	// StatefulSets is a list of StatefulSet objects to scale down to 0 replicas when in secondary mode.
	// +optional
	StatefulSets []string `json:"statefulSets,omitempty"`

	// CronJobs is a list of CronJob objects to suspend when in secondary mode.
	// +optional
	CronJobs []string `json:"cronJobs,omitempty"`
}

// VolumeReplicationStatus defines the status of a VolumeReplication
type VolumeReplicationStatus struct {
	Name           string `json:"name"`
	State          string `json:"state"`           // Current state
	Error          string `json:"error,omitempty"` // Only populated for errors
	Message        string `json:"message,omitempty"`
	LastUpdateTime string `json:"lastUpdateTime,omitempty"`
}

// WorkloadStatus defines the status of a managed workload
type WorkloadStatus struct {
	// Name of the workload
	Name string `json:"name"`

	// Kind of the workload (Deployment, StatefulSet, CronJob)
	Kind string `json:"kind"`

	// State indicates the current state of the workload (scaled down, suspended, etc.)
	State string `json:"state,omitempty"`

	// Error contains any error messages if the workload couldn't be managed properly
	Error string `json:"error,omitempty"`

	// LastUpdateTime is the time when the status was last updated
	LastUpdateTime string `json:"lastUpdateTime,omitempty"`
}

// FailoverPolicyStatus defines the observed state of FailoverPolicy
type FailoverPolicyStatus struct {
	// CurrentState reflects the actual failover state ("primary" or "secondary") of the system.
	CurrentState string `json:"currentState,omitempty"`

	// PendingVolumeReplicationUpdates represents the number of VolumeReplication objects
	// that still need to be updated to match the desired failover state.
	PendingVolumeReplicationUpdates int `json:"pendingVolumeReplicationUpdates,omitempty"`

	// Conditions represent the current state of failover reconciliation.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// VolumeReplicationStatuses contains status information for VolumeReplications
	VolumeReplicationStatuses []VolumeReplicationStatus `json:"volumeReplicationStatuses,omitempty"`

	// WorkloadStatus indicates whether workloads have been properly scaled/suspended
	// +optional
	WorkloadStatus string `json:"workloadStatus,omitempty"`

	// WorkloadStatuses contains detailed status information for individual workloads
	// +optional
	WorkloadStatuses []WorkloadStatus `json:"workloadStatuses,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// FailoverPolicy is the Schema for the failoverpolicies API
type FailoverPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   FailoverPolicySpec   `json:"spec,omitempty"`
	Status FailoverPolicyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// FailoverPolicyList contains a list of FailoverPolicy
type FailoverPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []FailoverPolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&FailoverPolicy{}, &FailoverPolicyList{})
}
