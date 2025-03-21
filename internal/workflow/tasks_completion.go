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
	"time"
)

//
// COMPLETION STAGE TASKS
//

// UpdateGlobalStateTask updates the global state in DynamoDB with new owner
type UpdateGlobalStateTask struct {
	BaseTask
}

// NewUpdateGlobalStateTask creates a new UpdateGlobalStateTask
func NewUpdateGlobalStateTask(ctx *WorkflowContext) *UpdateGlobalStateTask {
	return &UpdateGlobalStateTask{
		BaseTask: BaseTask{
			TaskName:        "UpdateGlobalState",
			TaskDescription: "Updates the DynamoDB global state with new owner information",
			Context:         ctx,
		},
	}
}

// Execute performs the task
func (t *UpdateGlobalStateTask) Execute(ctx context.Context) error {
	if t.DynamoDBManager == nil {
		return fmt.Errorf("DynamoDB manager not initialized")
	}

	failoverGroup := t.Context.FailoverGroup
	failover := t.Context.Failover
	targetCluster := t.Context.TargetClusterName

	// Update the global state in DynamoDB
	t.Logger.Info("Updating global state in DynamoDB",
		"namespace", failoverGroup.Namespace,
		"name", failoverGroup.Name,
		"targetCluster", targetCluster,
		"mode", failover.Spec.FailoverMode)

	// Attempt to update global state - this would depend on DynamoDB implementation
	// For demonstration, just log the attempt
	t.Logger.Info("Global state would be updated with new owner information")

	return nil
}

// RecordFailoverHistoryTask records the failover operation in DynamoDB history
type RecordFailoverHistoryTask struct {
	BaseTask
}

// NewRecordFailoverHistoryTask creates a new RecordFailoverHistoryTask
func NewRecordFailoverHistoryTask(ctx *WorkflowContext) *RecordFailoverHistoryTask {
	return &RecordFailoverHistoryTask{
		BaseTask: BaseTask{
			TaskName:        "RecordFailoverHistory",
			TaskDescription: "Records the failover operation in DynamoDB history",
			Context:         ctx,
		},
	}
}

// Execute performs the task
func (t *RecordFailoverHistoryTask) Execute(ctx context.Context) error {
	if t.DynamoDBManager == nil {
		return fmt.Errorf("DynamoDB manager not initialized")
	}

	failover := t.Context.Failover
	failoverGroup := t.Context.FailoverGroup

	// Calculate total duration
	totalDuration := time.Since(t.Context.StartTime)
	t.Context.Metrics.TotalFailoverTimeSeconds = int64(totalDuration.Seconds())

	// Record failover history in DynamoDB
	t.Logger.Info("Recording failover history in DynamoDB",
		"namespace", failoverGroup.Namespace,
		"name", failoverGroup.Name,
		"durationSeconds", totalDuration.Seconds(),
		"targetCluster", t.Context.TargetClusterName,
		"mode", failover.Spec.FailoverMode)

	// Attempt to record history - this would depend on DynamoDB implementation
	// For demonstration, just log the attempt
	t.Logger.Info("Failover history would be recorded in DynamoDB")

	return nil
}

// ReleaseLockTask releases the lock in DynamoDB
type ReleaseLockTask struct {
	BaseTask
}

// NewReleaseLockTask creates a new ReleaseLockTask
func NewReleaseLockTask(ctx *WorkflowContext) *ReleaseLockTask {
	return &ReleaseLockTask{
		BaseTask: BaseTask{
			TaskName:        "ReleaseLock",
			TaskDescription: "Releases the lock in DynamoDB for the failover group",
			Context:         ctx,
		},
	}
}

// Execute performs the task
func (t *ReleaseLockTask) Execute(ctx context.Context) error {
	if t.DynamoDBManager == nil {
		return fmt.Errorf("DynamoDB manager not initialized")
	}

	failoverGroup := t.Context.FailoverGroup

	// Check if we have a lease token
	if t.Context.LeaseToken == "" {
		t.Logger.Info("No lease token available, cannot release lock",
			"namespace", failoverGroup.Namespace,
			"name", failoverGroup.Name)
		return nil // Not a failure, we just don't have a lock to release
	}

	t.Logger.Info("Releasing lock in DynamoDB",
		"namespace", failoverGroup.Namespace,
		"name", failoverGroup.Name)

	// Attempt to release the lock using the stored lease token
	err := t.DynamoDBManager.ReleaseLock(ctx, failoverGroup.Namespace, failoverGroup.Name, t.Context.LeaseToken)
	if err != nil {
		return fmt.Errorf("failed to release lock: %w", err)
	}

	t.Logger.Info("Successfully released lock for failover operation")
	return nil
}
