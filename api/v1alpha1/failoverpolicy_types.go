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

// FailoverStateSpec defines the desired state of FailoverState
type FailoverPolicySpec struct {
	// FailoverState determines whether this resource should be in "primary" or "secondary" mode.
	// +kubebuilder:validation:Enum=primary;secondary
	FailoverState string `json:"failoverState"`

	// VolumeReplications is a list of VolumeReplication objects to manage in this failover state.
	// +kubebuilder:validation:MinItems=1
	VolumeReplications []string `json:"volumeReplications"`

	// VirtualServices is a list of VirtualService objects to update during failover.
	// +kubebuilder:validation:MinItems=1
	VirtualServices []string `json:"virtualServices"`
}

// FailoverStateStatus defines the observed state of FailoverState
type FailoverPolicyStatus struct {
	// AppliedState is the currently applied failover state ("primary" or "secondary").
	AppliedState string `json:"appliedState,omitempty"`

	// PendingVolumeReplicationUpdates represents the number of VolumeReplication objects
	// that still need to be updated to match the desired failover state.
	PendingVolumeReplicationUpdates int `json:"pendingVolumeReplicationUpdates,omitempty"`

	// Conditions represent the current state of failover reconciliation.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// FailoverState is the Schema for the failoverstates API
type FailoverPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   FailoverPolicySpec   `json:"spec,omitempty"`
	Status FailoverPolicyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// FailoverPolicyList contains a list of FailoverState
type FailoverPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []FailoverPolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&FailoverPolicy{}, &FailoverPolicyList{})
}
