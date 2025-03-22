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
	"fmt"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/christensenjairus/Failover-Operator/internal/controller/dynamodb"
)

// FailoverDependencyInjector implements DependencyInjector for failover tasks
type FailoverDependencyInjector struct {
	// Client is the Kubernetes client
	Client client.Client
	// Logger for tasks
	Logger logr.Logger
	// DynamoDBManager for DynamoDB operations
	DynamoDBManager *dynamodb.DynamoDBService
}

// NewFailoverDependencyInjector creates a new FailoverDependencyInjector
func NewFailoverDependencyInjector(
	client client.Client,
	logger logr.Logger,
	dynamoDBManager *dynamodb.DynamoDBService,
) *FailoverDependencyInjector {
	return &FailoverDependencyInjector{
		Client:          client,
		Logger:          logger,
		DynamoDBManager: dynamoDBManager,
	}
}

// InjectDependencies injects dependencies into the task
func (i *FailoverDependencyInjector) InjectDependencies(task WorkflowTask) error {
	// Type-assert to BaseTask
	baseTask, ok := task.(interface {
		GetName() string
		GetDescription() string
	})
	if !ok {
		return fmt.Errorf("task does not implement GetName/GetDescription methods")
	}

	// All tasks get the logger
	switch t := task.(type) {
	case *ValidateFailoverTask:
		t.Client = i.Client
		t.Logger = i.Logger.WithValues("task", baseTask.GetName())
	case *AcquireLockTask:
		t.DynamoDBManager = i.DynamoDBManager
		t.Logger = i.Logger.WithValues("task", baseTask.GetName())
	case *UpdateNetworkResourcesTask:
		t.Client = i.Client
		t.Logger = i.Logger.WithValues("task", baseTask.GetName())
	case *ScaleDownWorkloadsTask:
		t.Client = i.Client
		t.Logger = i.Logger.WithValues("task", baseTask.GetName())
	case *DisableFluxReconciliationTask:
		t.Client = i.Client
		t.Logger = i.Logger.WithValues("task", baseTask.GetName())
	case *WaitForWorkloadsScaledDownTask:
		t.Client = i.Client
		t.Logger = i.Logger.WithValues("task", baseTask.GetName())
	case *DemoteVolumesTask:
		t.Client = i.Client
		t.Logger = i.Logger.WithValues("task", baseTask.GetName())
	case *WaitForVolumesDemotedTask:
		t.Client = i.Client
		t.Logger = i.Logger.WithValues("task", baseTask.GetName())
	case *MarkVolumesReadyForPromotionTask:
		t.Client = i.Client
		t.DynamoDBManager = i.DynamoDBManager
		t.Logger = i.Logger.WithValues("task", baseTask.GetName())
	case *WaitForVolumesReadyForPromotionTask:
		t.Client = i.Client
		t.DynamoDBManager = i.DynamoDBManager
		t.Logger = i.Logger.WithValues("task", baseTask.GetName())
	case *PromoteVolumesTask:
		t.Client = i.Client
		t.Logger = i.Logger.WithValues("task", baseTask.GetName())
	case *WaitForVolumesPromotedTask:
		t.Client = i.Client
		t.Logger = i.Logger.WithValues("task", baseTask.GetName())
	case *VerifyDataAvailabilityTask:
		t.Client = i.Client
		t.Logger = i.Logger.WithValues("task", baseTask.GetName())
	case *ScaleUpWorkloadsTask:
		t.Client = i.Client
		t.Logger = i.Logger.WithValues("task", baseTask.GetName())
	case *TriggerFluxReconciliationTask:
		t.Client = i.Client
		t.Logger = i.Logger.WithValues("task", baseTask.GetName())
	case *WaitForTargetReadyTask:
		t.Client = i.Client
		t.Logger = i.Logger.WithValues("task", baseTask.GetName())
	case *MarkTargetReadyForTrafficTask:
		t.Client = i.Client
		t.DynamoDBManager = i.DynamoDBManager
		t.Logger = i.Logger.WithValues("task", baseTask.GetName())
	case *WaitForTargetReadyForTrafficTask:
		t.Client = i.Client
		t.DynamoDBManager = i.DynamoDBManager
		t.Logger = i.Logger.WithValues("task", baseTask.GetName())
	case *MarkTrafficTransitionCompleteTask:
		t.Client = i.Client
		t.DynamoDBManager = i.DynamoDBManager
		t.Logger = i.Logger.WithValues("task", baseTask.GetName())
	case *WaitForTrafficTransitionCompleteTask:
		t.Client = i.Client
		t.DynamoDBManager = i.DynamoDBManager
		t.Logger = i.Logger.WithValues("task", baseTask.GetName())
	case *UpdateGlobalStateTask:
		t.DynamoDBManager = i.DynamoDBManager
		t.Logger = i.Logger.WithValues("task", baseTask.GetName())
	case *RecordFailoverHistoryTask:
		t.DynamoDBManager = i.DynamoDBManager
		t.Logger = i.Logger.WithValues("task", baseTask.GetName())
	case *ReleaseLockTask:
		t.DynamoDBManager = i.DynamoDBManager
		t.Logger = i.Logger.WithValues("task", baseTask.GetName())
	case *UpdateWorkflowStateTask:
		t.Client = i.Client
		t.Logger = i.Logger.WithValues("task", baseTask.GetName())
	case *ResetWorkflowStateTask:
		t.Client = i.Client
		t.Logger = i.Logger.WithValues("task", baseTask.GetName())
	case *SetWorkflowPhaseTask:
		t.Client = i.Client
		t.Logger = i.Logger.WithValues("task", baseTask.GetName())
	// ... other cases for various task types ...
	default:
		// For task types we don't explicitly handle, just log a warning
		i.Logger.Info("No specific dependency injection handler for task type",
			"taskType", fmt.Sprintf("%T", task),
			"taskName", baseTask.GetName())
	}

	return nil
}

// GetBaseTask provides access to the BaseTask for dependency injection
func (t *ValidateFailoverTask) GetBaseTask() *BaseTask {
	return &t.BaseTask
}

// GetBaseTask provides access to the BaseTask for dependency injection
func (t *AcquireLockTask) GetBaseTask() *BaseTask {
	return &t.BaseTask
}

// GetBaseTask provides access to the BaseTask for dependency injection
func (t *UpdateNetworkResourcesTask) GetBaseTask() *BaseTask {
	return &t.BaseTask
}

// GetBaseTask provides access to the BaseTask for dependency injection
func (t *ScaleDownWorkloadsTask) GetBaseTask() *BaseTask {
	return &t.BaseTask
}

// GetBaseTask provides access to the BaseTask for dependency injection
func (t *DisableFluxReconciliationTask) GetBaseTask() *BaseTask {
	return &t.BaseTask
}

// GetBaseTask provides access to the BaseTask for dependency injection
func (t *WaitForWorkloadsScaledDownTask) GetBaseTask() *BaseTask {
	return &t.BaseTask
}

// GetBaseTask provides access to the BaseTask for dependency injection
func (t *DemoteVolumesTask) GetBaseTask() *BaseTask {
	return &t.BaseTask
}

// GetBaseTask provides access to the BaseTask for dependency injection
func (t *WaitForVolumesDemotedTask) GetBaseTask() *BaseTask {
	return &t.BaseTask
}

// GetBaseTask provides access to the BaseTask for dependency injection
func (t *MarkVolumesReadyForPromotionTask) GetBaseTask() *BaseTask {
	return &t.BaseTask
}

// GetBaseTask provides access to the BaseTask for dependency injection
func (t *WaitForVolumesReadyForPromotionTask) GetBaseTask() *BaseTask {
	return &t.BaseTask
}

// GetBaseTask provides access to the BaseTask for dependency injection
func (t *PromoteVolumesTask) GetBaseTask() *BaseTask {
	return &t.BaseTask
}

// GetBaseTask provides access to the BaseTask for dependency injection
func (t *WaitForVolumesPromotedTask) GetBaseTask() *BaseTask {
	return &t.BaseTask
}

// GetBaseTask provides access to the BaseTask for dependency injection
func (t *VerifyDataAvailabilityTask) GetBaseTask() *BaseTask {
	return &t.BaseTask
}

// GetBaseTask provides access to the BaseTask for dependency injection
func (t *ScaleUpWorkloadsTask) GetBaseTask() *BaseTask {
	return &t.BaseTask
}

// GetBaseTask provides access to the BaseTask for dependency injection
func (t *TriggerFluxReconciliationTask) GetBaseTask() *BaseTask {
	return &t.BaseTask
}

// GetBaseTask provides access to the BaseTask for dependency injection
func (t *WaitForTargetReadyTask) GetBaseTask() *BaseTask {
	return &t.BaseTask
}

// GetBaseTask provides access to the BaseTask for dependency injection
func (t *MarkTargetReadyForTrafficTask) GetBaseTask() *BaseTask {
	return &t.BaseTask
}

// GetBaseTask provides access to the BaseTask for dependency injection
func (t *WaitForTargetReadyForTrafficTask) GetBaseTask() *BaseTask {
	return &t.BaseTask
}

// GetBaseTask provides access to the BaseTask for dependency injection
func (t *MarkTrafficTransitionCompleteTask) GetBaseTask() *BaseTask {
	return &t.BaseTask
}

// GetBaseTask provides access to the BaseTask for dependency injection
func (t *WaitForTrafficTransitionCompleteTask) GetBaseTask() *BaseTask {
	return &t.BaseTask
}

// GetBaseTask provides access to the BaseTask for dependency injection
func (t *UpdateGlobalStateTask) GetBaseTask() *BaseTask {
	return &t.BaseTask
}

// GetBaseTask provides access to the BaseTask for dependency injection
func (t *RecordFailoverHistoryTask) GetBaseTask() *BaseTask {
	return &t.BaseTask
}

// GetBaseTask provides access to the BaseTask for dependency injection
func (t *ReleaseLockTask) GetBaseTask() *BaseTask {
	return &t.BaseTask
}
