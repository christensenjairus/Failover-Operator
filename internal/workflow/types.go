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

package workflow

import (
	"context"
	"time"

	crdv1alpha1 "github.com/christensenjairus/Failover-Operator/api/v1alpha1"
)

// WorkflowTask represents a single task in a workflow
type WorkflowTask interface {
	// Execute performs the task
	Execute(ctx context.Context) error
	// GetName returns the name of the task
	GetName() string
	// GetDescription returns a description of what the task does
	GetDescription() string
}

// WorkflowStage represents a stage in the workflow
type WorkflowStage struct {
	// Name of the stage
	Name string
	// Description of what this stage does
	Description string
	// Tasks to execute in this stage
	Tasks []WorkflowTask
}

// WorkflowContext contains data passed between workflow stages
type WorkflowContext struct {
	// Failover is the failover resource being processed
	Failover *crdv1alpha1.Failover
	// FailoverGroup is the failover group being processed
	FailoverGroup *crdv1alpha1.FailoverGroup
	// ClusterName is the name of the current cluster
	ClusterName string
	// IsSourceCluster indicates if this is the source cluster
	IsSourceCluster bool
	// IsTargetCluster indicates if this is the target cluster
	IsTargetCluster bool
	// SourceClusterName is the name of the source cluster
	SourceClusterName string
	// TargetClusterName is the name of the target cluster
	TargetClusterName string
	// FailoverMode indicates which failover mode is being used
	// CONSISTENCY: Prioritizes data consistency by shutting down source first before activating target
	// UPTIME: Prioritizes service uptime by activating target before deactivating source
	FailoverMode string
	// ForceFailover indicates if safety checks should be skipped
	ForceFailover bool
	// Metrics collects metrics during the failover
	Metrics *crdv1alpha1.FailoverMetrics
	// StartTime when the workflow started
	StartTime time.Time
	// LeaseToken is the token for DynamoDB lock
	LeaseToken string
	// Results is a map to store task results
	Results map[string]interface{}
}

// Workflow represents the overall failover workflow
type Workflow struct {
	// Name of the workflow
	Name string
	// Description of what this workflow does
	Description string
	// Stages to execute in sequence
	Stages []*WorkflowStage
	// Context data shared across stages
	Context *WorkflowContext
}

// NewWorkflowContext creates a new workflow context
func NewWorkflowContext(failover *crdv1alpha1.Failover, failoverGroup *crdv1alpha1.FailoverGroup, clusterName string) *WorkflowContext {
	sourceCluster := ""
	for _, c := range failoverGroup.Status.GlobalState.Clusters {
		if c.Role == "PRIMARY" {
			sourceCluster = c.Name
			break
		}
	}

	return &WorkflowContext{
		Failover:          failover,
		FailoverGroup:     failoverGroup,
		ClusterName:       clusterName,
		IsSourceCluster:   clusterName == sourceCluster,
		IsTargetCluster:   clusterName == failover.Spec.TargetCluster,
		SourceClusterName: sourceCluster,
		TargetClusterName: failover.Spec.TargetCluster,
		FailoverMode:      failover.Spec.FailoverMode,
		ForceFailover:     failover.Spec.Force,
		Metrics:           &crdv1alpha1.FailoverMetrics{},
		StartTime:         time.Now(),
		LeaseToken:        "",
		Results:           make(map[string]interface{}),
	}
}
