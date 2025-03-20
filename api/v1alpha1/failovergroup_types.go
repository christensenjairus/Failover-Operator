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

// FailoverGroupSpec defines the desired state of FailoverGroup
type FailoverGroupSpec struct {
	// DefaultFailoverMode determines the default failover approach for all components:
	// "safe" ensures data is fully synced before failover,
	// while "fast" allows immediate transition without waiting.
	// +kubebuilder:validation:Enum=safe;fast
	// +kubebuilder:validation:Required
	DefaultFailoverMode string `json:"defaultFailoverMode"`

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

	// Conditions represent the current state of failover reconciliation.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:printcolumn:name="State",type=string,JSONPath=`.status.state`
//+kubebuilder:printcolumn:name="Health",type=string,JSONPath=`.status.health`
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
