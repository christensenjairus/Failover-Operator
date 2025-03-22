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

// InjectDependencies injects dependencies into tasks
func (i *FailoverDependencyInjector) InjectDependencies(task WorkflowTask) error {
	// Type assert to BaseTask
	if baseTask, ok := task.(interface{ GetBaseTask() *BaseTask }); ok {
		bt := baseTask.GetBaseTask()
		bt.Client = i.Client
		bt.Logger = i.Logger
		bt.DynamoDBManager = i.DynamoDBManager
		return nil
	}
	return fmt.Errorf("task does not implement GetBaseTask method")
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

// GetClient returns the Kubernetes client
func (i *FailoverDependencyInjector) GetClient() client.Client {
	return i.Client
}
