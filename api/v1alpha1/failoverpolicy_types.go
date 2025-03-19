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

// ResourceReference defines a reference to a Kubernetes resource by name and optional namespace
// Deprecated: Use ManagedResource instead
type ResourceReference struct {
	// Name of the resource
	Name string `json:"name"`

	// Namespace of the resource (optional)
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// SupportedResourceKind represents a supported resource kind and its associated API group
type SupportedResourceKind struct {
	// Kind is the resource kind
	Kind string
	// APIGroup is the resource API group (without version)
	APIGroup string
	// DefaultAPIGroup is used when APIGroup is omitted
	DefaultAPIGroup string
}

// Define supported resource kinds as constants
// Note: These constants help maintain a single source of truth for supported resources
var (
	// VolumeReplicationKind represents VolumeReplication resources
	VolumeReplicationKind = SupportedResourceKind{
		Kind:            "VolumeReplication",
		APIGroup:        "replication.storage.openshift.io",
		DefaultAPIGroup: "replication.storage.openshift.io",
	}

	// DeploymentKind represents Deployment resources
	DeploymentKind = SupportedResourceKind{
		Kind:            "Deployment",
		APIGroup:        "apps",
		DefaultAPIGroup: "apps",
	}

	// StatefulSetKind represents StatefulSet resources
	StatefulSetKind = SupportedResourceKind{
		Kind:            "StatefulSet",
		APIGroup:        "apps",
		DefaultAPIGroup: "apps",
	}

	// CronJobKind represents CronJob resources
	CronJobKind = SupportedResourceKind{
		Kind:            "CronJob",
		APIGroup:        "batch",
		DefaultAPIGroup: "batch",
	}

	// VirtualServiceKind represents VirtualService resources
	VirtualServiceKind = SupportedResourceKind{
		Kind:            "VirtualService",
		APIGroup:        "networking.istio.io",
		DefaultAPIGroup: "networking.istio.io",
	}

	// HelmReleaseKind represents HelmRelease resources
	HelmReleaseKind = SupportedResourceKind{
		Kind:            "HelmRelease",
		APIGroup:        "helm.toolkit.fluxcd.io",
		DefaultAPIGroup: "helm.toolkit.fluxcd.io",
	}

	// KustomizationKind represents Kustomization resources
	KustomizationKind = SupportedResourceKind{
		Kind:            "Kustomization",
		APIGroup:        "kustomize.toolkit.fluxcd.io",
		DefaultAPIGroup: "kustomize.toolkit.fluxcd.io",
	}
)

// AllSupportedResourceKinds defines all resource kinds supported by the operator
var AllSupportedResourceKinds = []SupportedResourceKind{
	VolumeReplicationKind,
	DeploymentKind,
	StatefulSetKind,
	CronJobKind,
	VirtualServiceKind,
	HelmReleaseKind,
	KustomizationKind,
}

// GetSupportedKinds returns a list of all supported resource kinds
func GetSupportedKinds() []string {
	kinds := make([]string, len(AllSupportedResourceKinds))
	for i, k := range AllSupportedResourceKinds {
		kinds[i] = k.Kind
	}
	return kinds
}

// GetSupportedAPIGroups returns a list of all supported API groups
func GetSupportedAPIGroups() []string {
	groups := make([]string, len(AllSupportedResourceKinds))
	for i, k := range AllSupportedResourceKinds {
		groups[i] = k.APIGroup
	}
	return groups
}

// ManagedResource defines a resource managed by the failover operator
type ManagedResource struct {
	// Name of the resource
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Namespace of the resource
	// If not provided, the FailoverPolicy's namespace will be used
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// Kind specifies the type of resource (e.g., Deployment, StatefulSet, CronJob)
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=VolumeReplication;Deployment;StatefulSet;CronJob;VirtualService;HelmRelease;Kustomization
	Kind string `json:"kind"`

	// APIGroup specifies the Kubernetes API group for this resource
	// If not provided, the default API group for the specified Kind will be used
	// +optional
	// +kubebuilder:validation:Enum=replication.storage.openshift.io;apps;batch;networking.istio.io;helm.toolkit.fluxcd.io;kustomize.toolkit.fluxcd.io
	APIGroup string `json:"apiGroup,omitempty"`
}

// ResourceRef defines a simple reference to a Kubernetes resource by kind and name
type ResourceRef struct {
	// Kind of the resource
	// +kubebuilder:validation:Required
	Kind string `json:"kind"`

	// Name of the resource
	// +kubebuilder:validation:Required
	Name string `json:"name"`
}

// ComponentSpec defines a component in the application being managed
type ComponentSpec struct {
	// Name of the component
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Workloads are the resources that make up this component
	// +optional
	Workloads []ResourceRef `json:"workloads,omitempty"`

	// VolumeReplications are the volume replications associated with this component
	// +optional
	VolumeReplications []string `json:"volumeReplications,omitempty"`

	// VirtualServices are the Istio virtual services associated with this component
	// +optional
	VirtualServices []string `json:"virtualServices,omitempty"`
}

// FailoverPolicySpec defines the desired state of FailoverPolicy
type FailoverPolicySpec struct {
	// FailoverMode determines the failover approach: "safe" ensures data is fully synced before failover,
	// while "fast" allows immediate transition without waiting.
	// +kubebuilder:validation:Enum=safe;fast
	// +kubebuilder:validation:Required
	FailoverMode string `json:"failoverMode"`

	// ParentFluxResources defines the Flux resources that need to be suspended during failover operations
	// +optional
	ParentFluxResources []ResourceRef `json:"parentFluxResources,omitempty"`

	// Components defines the application components and their associated resources
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Components []ComponentSpec `json:"components"`

	// ManagedResources is a list of resources to be managed by this failover policy.
	// Deprecated: Use Components instead
	// +optional
	ManagedResources []ManagedResource `json:"managedResources,omitempty"`

	// DesiredState represents the intended failover state ("active" or "passive").
	// Deprecated: Use metadata.annotations[failover-operator.hahomelabs.com/desired-state] instead
	// +kubebuilder:validation:Enum=active;passive
	// +optional
	DesiredState string `json:"desiredState,omitempty"`

	// Mode determines the failover approach. "safe" ensures VolumeReplication is fully synced before failover,
	// while "unsafe" allows immediate transition without waiting.
	// Deprecated: Use failoverMode instead
	// +kubebuilder:validation:Enum=safe;unsafe
	// +optional
	Mode string `json:"mode,omitempty"`

	// VolumeReplications is a list of VolumeReplication objects to manage in this failover policy.
	// Deprecated: Use components[].volumeReplications instead
	// +optional
	VolumeReplications []ResourceReference `json:"volumeReplications,omitempty"`

	// VirtualServices is a list of VirtualService objects to update during failover.
	// Deprecated: Use components[].virtualServices instead
	// +optional
	VirtualServices []ResourceReference `json:"virtualServices,omitempty"`

	// Deployments is a list of Deployment objects to scale down to 0 replicas when in passive mode.
	// Deprecated: Use components[].workloads instead
	// +optional
	Deployments []ResourceReference `json:"deployments,omitempty"`

	// StatefulSets is a list of StatefulSet objects to scale down to 0 replicas when in passive mode.
	// Deprecated: Use components[].workloads instead
	// +optional
	StatefulSets []ResourceReference `json:"statefulSets,omitempty"`

	// CronJobs is a list of CronJob objects to suspend when in passive mode.
	// Deprecated: Use components[].workloads instead
	// +optional
	CronJobs []ResourceReference `json:"cronJobs,omitempty"`

	// HelmReleases is a list of Flux HelmRelease objects to suspend when in passive mode.
	// Deprecated: Use parentFluxResources instead
	// +optional
	HelmReleases []ResourceReference `json:"helmReleases,omitempty"`

	// Kustomizations is a list of Flux Kustomization objects to suspend when in passive mode.
	// Deprecated: Use parentFluxResources instead
	// +optional
	Kustomizations []ResourceReference `json:"kustomizations,omitempty"`
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
	// CurrentState reflects the actual failover state ("active" or "passive") of the system.
	CurrentState string `json:"currentState,omitempty"`

	// Health indicates the overall health of the failover policy
	// +optional
	Health string `json:"health,omitempty"`

	// LastTransitionTime is the time when the last state transition occurred
	// +optional
	LastTransitionTime string `json:"lastTransitionTime,omitempty"`

	// LastTransitionReason is the reason for the last state transition
	// +optional
	LastTransitionReason string `json:"lastTransitionReason,omitempty"`

	// LastTransitionMessage is a human-readable message describing the last state transition
	// +optional
	LastTransitionMessage string `json:"lastTransitionMessage,omitempty"`

	// LastTransitionDetails contains detailed information about the last state transition
	// +optional
	LastTransitionDetails string `json:"lastTransitionDetails,omitempty"`

	// Conditions represent the current state of failover reconciliation.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// VolumeReplicationStatuses contains status information for VolumeReplications
	// +optional
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
