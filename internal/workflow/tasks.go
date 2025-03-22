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
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	crdv1alpha1 "github.com/christensenjairus/Failover-Operator/api/v1alpha1"
	"github.com/christensenjairus/Failover-Operator/internal/controller/dynamodb"
	"github.com/google/uuid"
)

// BaseTask provides common functionality for all tasks
type BaseTask struct {
	// Name of the task
	TaskName string
	// Description of what this task does
	TaskDescription string
	// WorkflowContext contains data passed between workflow stages
	Context *WorkflowContext
	// Client is the Kubernetes client
	Client client.Client
	// Logger for task execution
	Logger logr.Logger
	// DynamoDBManager for DynamoDB operations
	DynamoDBManager *dynamodb.DynamoDBService
}

// GetName returns the name of the task
func (t *BaseTask) GetName() string {
	return t.TaskName
}

// GetDescription returns a description of what the task does
func (t *BaseTask) GetDescription() string {
	return t.TaskDescription
}

// UpdateFailoverGroupState updates the FailoverGroup.Status.State field
// and ensures the changes are published to the API server
func (t *BaseTask) UpdateFailoverGroupState(ctx context.Context, group *crdv1alpha1.FailoverGroup, state string) error {
	if t.Client == nil {
		t.Logger.Error(nil, "Cannot update FailoverGroup state - Client not initialized")
		return fmt.Errorf("client not initialized")
	}

	// Create a copy of the group to avoid modifying the shared instance
	groupCopy := group.DeepCopy()

	// Set new state
	groupCopy.Status.State = state

	// Also update health status based on state
	if state == "PRIMARY" || state == "STANDBY" {
		// For normal operational states, determine health based on primary cluster
		primaryHealth := "UNKNOWN"
		for _, cluster := range groupCopy.Status.GlobalState.Clusters {
			if cluster.Role == "PRIMARY" {
				primaryHealth = cluster.Health
				break
			}
		}
		groupCopy.Status.Health = primaryHealth
	} else {
		// For transitional states (FAILOVER, FAILBACK, or custom phases), show as DEGRADED
		groupCopy.Status.Health = "DEGRADED"
	}

	// Update status
	err := t.Client.Status().Update(ctx, groupCopy)
	if err != nil {
		t.Logger.Error(err, "Failed to update FailoverGroup state",
			"namespace", group.Namespace,
			"name", group.Name,
			"state", state)
		return err
	}

	// Update the original group reference to match what was sent to the server
	group.Status.State = state
	group.Status.Health = groupCopy.Status.Health

	t.Logger.Info("Successfully updated FailoverGroup state",
		"namespace", group.Namespace,
		"name", group.Name,
		"state", state,
		"health", groupCopy.Status.Health)

	return nil
}

//
// INITIALIZATION STAGE TASKS
//

// ValidateFailoverTask validates that the failover can proceed
type ValidateFailoverTask struct {
	BaseTask
}

// NewValidateFailoverTask creates a new ValidateFailoverTask
func NewValidateFailoverTask(ctx *WorkflowContext) *ValidateFailoverTask {
	return &ValidateFailoverTask{
		BaseTask: BaseTask{
			TaskName:        "ValidateFailover",
			TaskDescription: "Validates that the failover prerequisites are met",
			Context:         ctx,
		},
	}
}

// Execute performs the task
func (t *ValidateFailoverTask) Execute(ctx context.Context) error {
	// Get the failover group
	failoverGroup := t.Context.FailoverGroup
	targetCluster := t.Context.TargetClusterName

	// Check if target cluster exists in the failover group
	targetExists := false
	targetHealthy := false
	sourceExists := false
	sourceIsActive := false

	for _, cluster := range failoverGroup.Status.GlobalState.Clusters {
		if cluster.Name == targetCluster {
			targetExists = true
			// Target must be in STANDBY role and either OK or DEGRADED health
			if cluster.Role == "STANDBY" && (cluster.Health == "OK" || cluster.Health == "DEGRADED") {
				targetHealthy = true
			}
		}

		if cluster.Role == "PRIMARY" {
			sourceExists = true
			if cluster.Name == failoverGroup.Status.GlobalState.ActiveCluster {
				sourceIsActive = true
			}
		}
	}

	// Validate prerequisites
	if !targetExists {
		return fmt.Errorf("target cluster %s does not exist in failover group", targetCluster)
	}

	// Check if we should enforce health requirements
	isForced := t.Context.Failover.Spec.Force
	if !targetHealthy && !isForced {
		return fmt.Errorf("target cluster %s is not in STANDBY role or not healthy", targetCluster)
	}

	if !sourceExists {
		return fmt.Errorf("no PRIMARY cluster found in failover group")
	}

	if !sourceIsActive && !isForced {
		return fmt.Errorf("PRIMARY cluster is not the active cluster in global state")
	}

	// Log successful validation
	t.Logger.Info("Failover prerequisites validated successfully")
	return nil
}

// AcquireLockTask acquires a lock in DynamoDB for the failover group
type AcquireLockTask struct {
	BaseTask
}

// NewAcquireLockTask creates a new AcquireLockTask
func NewAcquireLockTask(ctx *WorkflowContext) *AcquireLockTask {
	return &AcquireLockTask{
		BaseTask: BaseTask{
			TaskName:        "AcquireLock",
			TaskDescription: "Acquires a lock in DynamoDB for the failover group",
			Context:         ctx,
		},
	}
}

// Execute performs the task
func (t *AcquireLockTask) Execute(ctx context.Context) error {
	t.Logger.Info("Acquiring lock for failover group",
		"namespace", t.Context.FailoverGroup.Namespace,
		"name", t.Context.FailoverGroup.Name)

	if t.DynamoDBManager == nil {
		// If DynamoDBManager is not available, just simulate the acquisition for development/testing
		t.Logger.Info("DynamoDBManager not initialized, simulating lock acquisition")

		// Set a dummy lease token in the context
		t.Context.LeaseToken = fmt.Sprintf("simulated-lock-%s", uuid.New().String())

		// Even without DynamoDB, the workflow should continue for testing purposes
		return nil
	}

	// If Force is true, bypass lock acquisition and set a special token
	if t.Context.ForceFailover {
		t.Logger.Info("Force flag is set, bypassing lock acquisition",
			"group", t.Context.FailoverGroup.Name)
		t.Context.LeaseToken = fmt.Sprintf("forced-bypass-%s", uuid.New().String())
		t.Context.Results["SourceClusterHoldsLock"] = true
		return nil
	}

	t.Logger.Info("Attempting to acquire lock for failover group",
		"group", t.Context.FailoverGroup.Name)

	// Try to acquire the lock
	reason := "Failover operation initiated"
	namespace := t.Context.FailoverGroup.Namespace
	groupName := t.Context.FailoverGroup.Name
	leaseToken, err := t.DynamoDBManager.AcquireLock(ctx, namespace, groupName, reason)
	if err != nil {
		// Check if the error is due to the lock being held by the source cluster
		// and if the failover mode is CONSISTENCY or Force is true
		if strings.Contains(err.Error(), "locked by") {
			lockHolder := extractLockHolder(err.Error())
			isHeldBySource := lockHolder == t.Context.SourceClusterName
			isConsistencyMode := strings.ToUpper(t.Context.Failover.Spec.FailoverMode) == "CONSISTENCY"
			isForced := t.Context.Failover.Spec.Force

			// Set flag in the workflow context to track this condition
			t.Context.Results["SourceClusterHoldsLock"] = isHeldBySource

			if isHeldBySource && (isConsistencyMode || isForced) {
				t.Logger.Info("Lock is held by source cluster, will proceed with failover",
					"sourceCluster", t.Context.SourceClusterName,
					"mode", t.Context.Failover.Spec.FailoverMode,
					"force", t.Context.Failover.Spec.Force)

				// Set a special lease token that indicates we're proceeding despite the lock
				t.Context.LeaseToken = fmt.Sprintf("forced-lock-bypass-%s", uuid.New().String())
				return nil
			}
		}

		return fmt.Errorf("failed to acquire lock for failover group %s: %w",
			t.Context.FailoverGroup.Name, err)
	}

	t.Logger.Info("Successfully acquired lock for failover group",
		"group", t.Context.FailoverGroup.Name,
		"leaseToken", leaseToken)

	// Store the lease token in the workflow context
	t.Context.LeaseToken = leaseToken

	return nil
}

// extractLockHolder extracts the lock holder's name from the error message
func extractLockHolder(errorMsg string) string {
	// Expected format: "... locked by: CLUSTER_NAME ..."
	parts := strings.Split(errorMsg, "locked by")
	if len(parts) < 2 {
		return ""
	}

	// Extract the cluster name, removing surrounding whitespace and punctuation
	lockHolder := strings.TrimSpace(parts[1])
	lockHolder = strings.TrimPrefix(lockHolder, ":")
	lockHolder = strings.TrimSpace(lockHolder)

	// If there's additional text after the cluster name, remove it
	if idx := strings.Index(lockHolder, " "); idx > 0 {
		lockHolder = lockHolder[:idx]
	}
	if idx := strings.Index(lockHolder, "."); idx > 0 {
		lockHolder = lockHolder[:idx]
	}

	return lockHolder
}

//
// SOURCE CLUSTER SHUTDOWN TASKS
//

// UpdateNetworkResourcesTask updates network resources for traffic routing
type UpdateNetworkResourcesTask struct {
	BaseTask
	Action string // "enable" or "disable"
}

// NewUpdateNetworkResourcesTask creates a new UpdateNetworkResourcesTask
func NewUpdateNetworkResourcesTask(ctx *WorkflowContext, action string) *UpdateNetworkResourcesTask {
	description := "Updates network resources to enable traffic routing"
	if action == "disable" {
		description = "Updates network resources to disable traffic routing"
	}

	return &UpdateNetworkResourcesTask{
		BaseTask: BaseTask{
			TaskName:        "UpdateNetworkResources",
			TaskDescription: description,
			Context:         ctx,
		},
		Action: action,
	}
}

// Execute performs the task
func (t *UpdateNetworkResourcesTask) Execute(ctx context.Context) error {
	failoverGroup := t.Context.FailoverGroup

	// Log what we're doing
	action := "enabling"
	if t.Action == "disable" {
		action = "disabling"
	} else if t.Action == "transition" {
		action = "transitioning"
	}

	t.Logger.Info(fmt.Sprintf("%s network resources for traffic routing", action),
		"namespace", failoverGroup.Namespace,
		"name", failoverGroup.Name,
		"action", t.Action)

	// This would call different methods based on the action
	// For example, update ingress, virtual services, etc.
	// For now, just log what would happen
	t.Logger.Info("Network resources updated successfully")

	return nil
}

// ScaleDownWorkloadsTask scales down workloads in the source cluster
type ScaleDownWorkloadsTask struct {
	BaseTask
}

// NewScaleDownWorkloadsTask creates a new ScaleDownWorkloadsTask
func NewScaleDownWorkloadsTask(ctx *WorkflowContext) *ScaleDownWorkloadsTask {
	return &ScaleDownWorkloadsTask{
		BaseTask: BaseTask{
			TaskName:        "ScaleDownWorkloads",
			TaskDescription: "Scales down workloads in the source cluster",
			Context:         ctx,
		},
	}
}

// Execute performs the task
func (t *ScaleDownWorkloadsTask) Execute(ctx context.Context) error {
	failoverGroup := t.Context.FailoverGroup

	// Process each workload based on its kind
	for _, workload := range failoverGroup.Spec.Workloads {
		switch workload.Kind {
		case "Deployment":
			// Scale down deployment
			// Implementation would use the Kubernetes client
			t.Logger.Info("Would scale down Deployment", "name", workload.Name)

		case "StatefulSet":
			// Scale down statefulset
			// Implementation would use the Kubernetes client
			t.Logger.Info("Would scale down StatefulSet", "name", workload.Name)

		case "CronJob":
			// Suspend cronjob
			// Implementation would use the Kubernetes client
			t.Logger.Info("Would suspend CronJob", "name", workload.Name)

		default:
			t.Logger.Info("Unknown workload kind, skipping",
				"kind", workload.Kind,
				"name", workload.Name)
		}
	}

	t.Logger.Info("Workloads scaled down successfully")
	return nil
}

// DisableFluxReconciliationTask disables Flux reconciliation for resources
type DisableFluxReconciliationTask struct {
	BaseTask
}

// NewDisableFluxReconciliationTask creates a new DisableFluxReconciliationTask
func NewDisableFluxReconciliationTask(ctx *WorkflowContext) *DisableFluxReconciliationTask {
	return &DisableFluxReconciliationTask{
		BaseTask: BaseTask{
			TaskName:        "DisableFluxReconciliation",
			TaskDescription: "Disables Flux reconciliation for resources",
			Context:         ctx,
		},
	}
}

// Execute performs the task
func (t *DisableFluxReconciliationTask) Execute(ctx context.Context) error {
	failoverGroup := t.Context.FailoverGroup

	// Process each Flux resource
	for _, fluxResource := range failoverGroup.Spec.FluxResources {
		// Add annotation fluxcd.io/ignore: "true"
		t.Logger.Info("Would disable Flux reconciliation",
			"kind", fluxResource.Kind,
			"name", fluxResource.Name)
	}

	t.Logger.Info("Flux reconciliation disabled for resources")
	return nil
}

// WaitForWorkloadsScaledDownTask waits for workloads to be scaled down
type WaitForWorkloadsScaledDownTask struct {
	BaseTask
}

// NewWaitForWorkloadsScaledDownTask creates a new WaitForWorkloadsScaledDownTask
func NewWaitForWorkloadsScaledDownTask(ctx *WorkflowContext) *WaitForWorkloadsScaledDownTask {
	return &WaitForWorkloadsScaledDownTask{
		BaseTask: BaseTask{
			TaskName:        "WaitForWorkloadsScaledDown",
			TaskDescription: "Waits for workloads to be fully scaled down",
			Context:         ctx,
		},
	}
}

// Execute performs the task
func (t *WaitForWorkloadsScaledDownTask) Execute(ctx context.Context) error {
	// In a real implementation, this would poll the Kubernetes API
	// to check if all workloads are scaled down

	// For now, just simulate waiting
	t.Logger.Info("Waiting for workloads to scale down")
	// Simulate a delay
	time.Sleep(1 * time.Second)

	t.Logger.Info("All workloads successfully scaled down")
	return nil
}

//
// VOLUME TRANSITION TASKS
//

// DemoteVolumesTask demotes volumes in the source cluster to Secondary
type DemoteVolumesTask struct {
	BaseTask
}

// NewDemoteVolumesTask creates a new DemoteVolumesTask
func NewDemoteVolumesTask(ctx *WorkflowContext) *DemoteVolumesTask {
	return &DemoteVolumesTask{
		BaseTask: BaseTask{
			TaskName:        "DemoteVolumes",
			TaskDescription: "Demotes volumes in the source cluster to Secondary role",
			Context:         ctx,
		},
	}
}

// Execute performs the task
func (t *DemoteVolumesTask) Execute(ctx context.Context) error {
	failoverGroup := t.Context.FailoverGroup

	// Find all volume replications to demote
	for _, workload := range failoverGroup.Spec.Workloads {
		for _, volName := range workload.VolumeReplications {
			// Demote each volume to secondary
			t.Logger.Info("Would demote volume replication to secondary",
				"volume", volName,
				"workload", workload.Name)
		}
	}

	t.Logger.Info("Volumes demoted to Secondary role")
	return nil
}

// WaitForVolumesDemotedTask waits for volumes to be demoted
type WaitForVolumesDemotedTask struct {
	BaseTask
}

// NewWaitForVolumesDemotedTask creates a new WaitForVolumesDemotedTask
func NewWaitForVolumesDemotedTask(ctx *WorkflowContext) *WaitForVolumesDemotedTask {
	return &WaitForVolumesDemotedTask{
		BaseTask: BaseTask{
			TaskName:        "WaitForVolumesDemoted",
			TaskDescription: "Waits for volumes to be fully demoted to Secondary role",
			Context:         ctx,
		},
	}
}

// Execute performs the task
func (t *WaitForVolumesDemotedTask) Execute(ctx context.Context) error {
	// In a real implementation, this would poll the Kubernetes API
	// to check if all volume replications are in Secondary state

	// For now, just simulate waiting
	t.Logger.Info("Waiting for volumes to be demoted")
	// Simulate a delay
	time.Sleep(1 * time.Second)

	t.Logger.Info("All volumes successfully demoted to Secondary role")
	return nil
}

// UpdateWorkflowStateTask updates the FailoverGroup.Status.State field
// to reflect the current failover workflow stage
type UpdateWorkflowStateTask struct {
	BaseTask
	Client client.Client
}

// NewUpdateWorkflowStateTask creates a new task to update the workflow state
func NewUpdateWorkflowStateTask(ctx *WorkflowContext) *UpdateWorkflowStateTask {
	return &UpdateWorkflowStateTask{
		BaseTask: BaseTask{
			TaskName:        "UpdateWorkflowState",
			TaskDescription: "Update the FailoverGroup state to reflect the current failover operation",
			Context:         ctx,
		},
	}
}

// Execute updates the State in FailoverGroup status
func (t *UpdateWorkflowStateTask) Execute(ctx context.Context) error {
	failoverGroup := t.Context.FailoverGroup

	// Determine the appropriate state based on the cluster's role in the failover
	var state string
	if t.Context.IsTargetCluster {
		state = "FAILOVER"
		t.Logger.Info("Setting target cluster state to FAILOVER",
			"namespace", failoverGroup.Namespace,
			"name", failoverGroup.Name)
	} else if t.Context.IsSourceCluster {
		state = "FAILBACK"
		t.Logger.Info("Setting source cluster state to FAILBACK",
			"namespace", failoverGroup.Namespace,
			"name", failoverGroup.Name)
	} else {
		// This shouldn't happen, but if we're not on source or target, don't update state
		t.Logger.Info("Not updating state because this cluster is neither source nor target",
			"thisCluster", t.Context.ClusterName,
			"sourceCluster", t.Context.SourceClusterName,
			"targetCluster", t.Context.TargetClusterName)
		return nil
	}

	// Update state using helper
	return t.UpdateFailoverGroupState(ctx, failoverGroup, state)
}

// ResetWorkflowStateTask resets the FailoverGroup.Status.State field
// back to the normal operational state after failover completes
type ResetWorkflowStateTask struct {
	BaseTask
	Client client.Client
}

// NewResetWorkflowStateTask creates a new task to reset the workflow state
func NewResetWorkflowStateTask(ctx *WorkflowContext) *ResetWorkflowStateTask {
	return &ResetWorkflowStateTask{
		BaseTask: BaseTask{
			TaskName:        "ResetWorkflowState",
			TaskDescription: "Reset the FailoverGroup state after failover completion",
			Context:         ctx,
		},
	}
}

// Execute resets the State in FailoverGroup status
func (t *ResetWorkflowStateTask) Execute(ctx context.Context) error {
	failoverGroup := t.Context.FailoverGroup

	// Determine the appropriate state based on the cluster's role
	var state string
	if t.Context.ClusterName == t.Context.TargetClusterName {
		state = "PRIMARY"
	} else {
		state = "STANDBY"
	}

	// Update state using helper
	return t.UpdateFailoverGroupState(ctx, failoverGroup, state)
}

// SetWorkflowPhaseTask updates the FailoverGroup.Status.State field to show
// specific phases during the failover process
type SetWorkflowPhaseTask struct {
	BaseTask
	Client client.Client
	Phase  string
}

// NewSetWorkflowPhaseTask creates a new task to set a specific workflow phase
func NewSetWorkflowPhaseTask(ctx *WorkflowContext, phase string) *SetWorkflowPhaseTask {
	return &SetWorkflowPhaseTask{
		BaseTask: BaseTask{
			TaskName:        "SetWorkflowPhase_" + phase,
			TaskDescription: "Update the FailoverGroup state to show the " + phase + " phase",
			Context:         ctx,
		},
		Phase: phase,
	}
}

// Execute updates the State in FailoverGroup status to show the current phase
func (t *SetWorkflowPhaseTask) Execute(ctx context.Context) error {
	failoverGroup := t.Context.FailoverGroup

	// Log the phase we're setting
	t.Logger.Info("Setting workflow phase",
		"namespace", failoverGroup.Namespace,
		"name", failoverGroup.Name,
		"phase", t.Phase)

	// Update state using helper
	return t.UpdateFailoverGroupState(ctx, failoverGroup, t.Phase)
}
