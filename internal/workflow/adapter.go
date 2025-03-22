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

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	crdv1alpha1 "github.com/christensenjairus/Failover-Operator/api/v1alpha1"
	"github.com/christensenjairus/Failover-Operator/internal/controller/dynamodb"
)

// ManagerAdapter adapts between the failover controller and workflow engine
type ManagerAdapter struct {
	// Client is the Kubernetes client
	Client client.Client
	// Logger for the adapter
	Logger logr.Logger
	// ClusterName is the name of the current cluster
	ClusterName string
	// DynamoDBManager for DynamoDB operations
	DynamoDBManager *dynamodb.DynamoDBService
}

// NewManagerAdapter creates a new manager adapter
func NewManagerAdapter(
	client client.Client,
	logger logr.Logger,
	clusterName string,
	dynamoDBManager *dynamodb.DynamoDBService,
) *ManagerAdapter {
	return &ManagerAdapter{
		Client:          client,
		Logger:          logger,
		ClusterName:     clusterName,
		DynamoDBManager: dynamoDBManager,
	}
}

// ProcessFailover processes a failover request
func (a *ManagerAdapter) ProcessFailover(ctx context.Context, failover *crdv1alpha1.Failover, failoverGroup *crdv1alpha1.FailoverGroup) error {
	// First, explicitly set the state to IN_PROGRESS
	failover.Status.State = "IN_PROGRESS"
	if err := a.Client.Status().Update(ctx, failover); err != nil {
		a.Logger.Error(err, "Failed to update Failover initial state to IN_PROGRESS")
		// Continue anyway since this isn't critical
	} else {
		a.Logger.Info("Updated Failover initial state to IN_PROGRESS")
	}

	// Create dependency injector
	injector := NewFailoverDependencyInjector(a.Client, a.Logger, a.DynamoDBManager)

	// Create workflow context
	workflowCtx := NewWorkflowContext(failover, failoverGroup, a.ClusterName)

	// Determine mode
	mode := strings.ToUpper(failover.Spec.FailoverMode)

	// Create tasks based on the failover mode
	var tasks []Task
	if mode == "UPTIME" {
		tasks = a.createUptimeTasks(workflowCtx)
	} else {
		// Default to consistency mode
		tasks = a.createConsistencyTasks(workflowCtx)
	}

	// Create and configure the engine
	engine := NewEngine(tasks, a.Logger, injector)

	// Execute the workflow
	err := engine.Execute(ctx, workflowCtx)
	if err != nil {
		// Update the state to FAILED to ensure it's not stuck
		failover.Status.State = "FAILED"
		stateErr := a.Client.Status().Update(ctx, failover)
		if stateErr != nil {
			a.Logger.Error(stateErr, "Failed to update Failover state to FAILED")
		} else {
			a.Logger.Info("Updated Failover state to FAILED")
		}

		// Check if this was a lock-related error with the source cluster
		if workflowCtx.Results != nil {
			sourceHoldsLock, hasKey := workflowCtx.Results["SourceClusterHoldsLock"]
			if hasKey && sourceHoldsLock.(bool) && strings.Contains(err.Error(), "locked by") {
				// This is a known case where the source cluster holds the lock
				// The workflow tasks will have tried to handle this, but if we still
				// got an error, we should inform the user about the special case
				a.Logger.Info("Failover operation couldn't complete because the source cluster still holds the lock",
					"sourceCluster", workflowCtx.SourceClusterName,
					"targetCluster", workflowCtx.TargetClusterName)

				// Update failover status with a more helpful message
				updateErr := a.updateFailoverStatus(ctx, failover, "FAILED",
					fmt.Sprintf("Source cluster '%s' still holds the lock. This may happen if the source cluster is still active. "+
						"Verify the source cluster status and retry the failover with force=true if needed.",
						workflowCtx.SourceClusterName))
				if updateErr != nil {
					// Log the status update error but return the original error
					a.Logger.Error(updateErr, "Failed to update failover status after workflow error")
				}

				return fmt.Errorf("source cluster '%s' still holds the lock: %w", workflowCtx.SourceClusterName, err)
			}
		}

		// Standard error handling for other cases
		updateErr := a.updateFailoverStatus(ctx, failover, "FAILED", err.Error())
		if updateErr != nil {
			// Log the status update error but return the original error
			a.Logger.Error(updateErr, "Failed to update failover status after workflow error")
		}
		return fmt.Errorf("workflow execution failed: %w", err)
	}

	// Update metrics from workflow context
	failover.Status.Metrics = *workflowCtx.Metrics

	// Make sure the state is set to SUCCESS at the end of successful workflow
	failover.Status.State = "SUCCESS"
	if stateErr := a.Client.Status().Update(ctx, failover); stateErr != nil {
		a.Logger.Error(stateErr, "Failed to update final Failover state to SUCCESS")
		// Continue anyway to update the overall status
	} else {
		a.Logger.Info("Updated final Failover state to SUCCESS")
	}

	// Update failover status to reflect success
	if err := a.updateFailoverStatus(ctx, failover, "SUCCESS", ""); err != nil {
		return fmt.Errorf("failed to update failover status after successful workflow: %w", err)
	}

	return nil
}

// createConsistencyTasks creates tasks for the consistency mode
func (a *ManagerAdapter) createConsistencyTasks(ctx *WorkflowContext) []Task {
	return []Task{
		// Initialization stage
		NewValidateFailoverTask(ctx),
		NewAcquireLockTask(ctx),

		// Source cluster preparation
		NewUpdateNetworkResourcesTask(ctx, "disable"),
		NewScaleDownWorkloadsTask(ctx),
		NewDisableFluxReconciliationTask(ctx),
		NewWaitForWorkloadsScaledDownTask(ctx),

		// Volume demotion (source cluster)
		NewDemoteVolumesTask(ctx),
		NewWaitForVolumesDemotedTask(ctx),
		NewMarkVolumesReadyForPromotionTask(ctx),
		NewWaitForVolumesReadyForPromotionTask(ctx),

		// Volume promotion (target cluster)
		NewPromoteVolumesTask(ctx),
		NewWaitForVolumesPromotedTask(ctx),
		NewVerifyDataAvailabilityTask(ctx),

		// Target cluster activation
		NewScaleUpWorkloadsTask(ctx),
		NewTriggerFluxReconciliationTask(ctx),
		NewWaitForTargetReadyTask(ctx),
		NewUpdateNetworkResourcesTask(ctx, "enable"),

		// Completion
		NewUpdateGlobalStateTask(ctx),
		NewRecordFailoverHistoryTask(ctx),
		NewReleaseLockTask(ctx),
	}
}

// createUptimeTasks creates tasks for the uptime mode
func (a *ManagerAdapter) createUptimeTasks(ctx *WorkflowContext) []Task {
	return []Task{
		// Initialization stage
		NewValidateFailoverTask(ctx),
		NewAcquireLockTask(ctx),

		// Target preparation (bring up target first)
		NewPromoteVolumesTask(ctx),
		NewWaitForVolumesPromotedTask(ctx),
		NewVerifyDataAvailabilityTask(ctx),
		NewScaleUpWorkloadsTask(ctx),
		NewTriggerFluxReconciliationTask(ctx),
		NewWaitForTargetReadyTask(ctx),
		NewMarkTargetReadyForTrafficTask(ctx),
		NewWaitForTargetReadyForTrafficTask(ctx),

		// Traffic transition
		NewUpdateNetworkResourcesTask(ctx, "transition"),
		NewMarkTrafficTransitionCompleteTask(ctx),
		NewWaitForTrafficTransitionCompleteTask(ctx),

		// Source shutdown
		NewUpdateNetworkResourcesTask(ctx, "disable"),
		NewScaleDownWorkloadsTask(ctx),
		NewDisableFluxReconciliationTask(ctx),
		NewWaitForWorkloadsScaledDownTask(ctx),
		NewDemoteVolumesTask(ctx),
		NewWaitForVolumesDemotedTask(ctx),

		// Completion
		NewUpdateGlobalStateTask(ctx),
		NewRecordFailoverHistoryTask(ctx),
		NewReleaseLockTask(ctx),
	}
}

// updateFailoverStatus updates the status of the failover resource
func (a *ManagerAdapter) updateFailoverStatus(ctx context.Context, failover *crdv1alpha1.Failover, status string, message string) error {
	// Update status
	failover.Status.Status = status

	// Add condition based on status
	now := metav1.Now()
	condition := metav1.Condition{
		Type:               "Reconciled",
		Status:             "True",
		ObservedGeneration: failover.Generation,
		LastTransitionTime: now,
		Reason:             "FailoverProcessed",
		Message:            "Failover processed successfully",
	}

	if status == "FAILED" {
		condition.Status = "False"
		condition.Reason = "FailoverFailed"
		condition.Message = fmt.Sprintf("Failover failed: %s", message)
	}

	// Get current conditions
	conditions := failover.Status.Conditions

	// Update or add the condition
	newConditions := []metav1.Condition{}
	conditionExists := false
	for _, c := range conditions {
		if c.Type == condition.Type {
			newConditions = append(newConditions, condition)
			conditionExists = true
		} else {
			newConditions = append(newConditions, c)
		}
	}

	if !conditionExists {
		newConditions = append(newConditions, condition)
	}

	failover.Status.Conditions = newConditions

	// Update the failover status
	return a.Client.Status().Update(ctx, failover)
}
