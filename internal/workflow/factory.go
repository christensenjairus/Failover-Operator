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
	"strings"

	crdv1alpha1 "github.com/christensenjairus/Failover-Operator/api/v1alpha1"
	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Factory creates workflows
type Factory struct {
	// Client is the Kubernetes client
	Client client.Client
	// Logger for workflow creation
	Logger logr.Logger
	// ClusterName is the name of the current cluster
	ClusterName string
	// DependencyInjector provides components needed by tasks
	DependencyInjector DependencyInjector
}

// DependencyInjector injects dependencies into tasks
type DependencyInjector interface {
	// InjectDependencies injects dependencies into the task
	InjectDependencies(task WorkflowTask) error
	// GetClient returns the Kubernetes client
	GetClient() client.Client
}

// NewFactory creates a new workflow factory
func NewFactory(client client.Client, logger logr.Logger, clusterName string, injector DependencyInjector) *Factory {
	return &Factory{
		Client:             client,
		Logger:             logger,
		ClusterName:        clusterName,
		DependencyInjector: injector,
	}
}

// CreateWorkflow creates a workflow based on the failover request
func (f *Factory) CreateWorkflow(failover *crdv1alpha1.Failover, failoverGroup *crdv1alpha1.FailoverGroup) *Workflow {
	// Create workflow context
	ctx := NewWorkflowContext(failover, failoverGroup, f.ClusterName)

	// Normalize mode to uppercase
	mode := strings.ToUpper(failover.Spec.FailoverMode)

	var workflow *Workflow
	switch mode {
	case "CONSISTENCY":
		workflow = f.createConsistencyModeWorkflow(ctx)
	case "UPTIME":
		workflow = f.createUptimeModeWorkflow(ctx)
	default:
		// Default to CONSISTENCY mode for safety
		f.Logger.Info("Invalid failover mode specified, defaulting to CONSISTENCY mode", "specifiedMode", mode)
		workflow = f.createConsistencyModeWorkflow(ctx)
	}

	// Inject dependencies into all tasks
	for _, stage := range workflow.Stages {
		for _, task := range stage.Tasks {
			if f.DependencyInjector != nil {
				f.DependencyInjector.InjectDependencies(task)
			}
		}
	}

	return workflow
}

// createConsistencyModeWorkflow creates a workflow for CONSISTENCY mode failover
func (f *Factory) createConsistencyModeWorkflow(ctx *WorkflowContext) *Workflow {
	workflow := &Workflow{
		Name:        "ConsistencyModeFailover",
		Description: "Failover workflow that prioritizes data consistency by stopping the source cluster first",
		Context:     ctx,
		Stages:      []*WorkflowStage{},
	}

	// STAGE 1: INITIALIZATION
	initStage := &WorkflowStage{
		Name:        "Initialization",
		Description: "Prepare for failover by validating prerequisites and acquiring locks",
		Tasks:       []WorkflowTask{},
	}

	// Add initialization tasks with more detailed descriptions
	initStage.Tasks = append(initStage.Tasks, NewValidateFailoverTask(ctx))
	initStage.Tasks = append(initStage.Tasks, NewAcquireLockTask(ctx))
	workflow.Stages = append(workflow.Stages, initStage)

	// Tasks that are executed only on source cluster
	if ctx.IsSourceCluster {
		// STAGE 2: SOURCE CLUSTER SHUTDOWN
		sourceShutdownStage := &WorkflowStage{
			Name:        "SourceClusterShutdown",
			Description: "CONSISTENCY MODE: Deactivate the source cluster before activating target to ensure data integrity",
			Tasks:       []WorkflowTask{},
		}

		// Add source shutdown tasks
		sourceShutdownStage.Tasks = append(sourceShutdownStage.Tasks, NewUpdateNetworkResourcesTask(ctx, "disable"))
		sourceShutdownStage.Tasks = append(sourceShutdownStage.Tasks, NewScaleDownWorkloadsTask(ctx))
		sourceShutdownStage.Tasks = append(sourceShutdownStage.Tasks, NewDisableFluxReconciliationTask(ctx))
		sourceShutdownStage.Tasks = append(sourceShutdownStage.Tasks, NewWaitForWorkloadsScaledDownTask(ctx))
		workflow.Stages = append(workflow.Stages, sourceShutdownStage)

		// STAGE 3: VOLUME TRANSITION (SOURCE)
		volumeTransitionStage := &WorkflowStage{
			Name:        "VolumeTransitionSource",
			Description: "CONSISTENCY MODE: Demote volumes in source cluster and signal readiness for target promotion",
			Tasks:       []WorkflowTask{},
		}

		// Add volume transition tasks with more detailed descriptions for CONSISTENCY mode
		volumeTransitionStage.Tasks = append(volumeTransitionStage.Tasks, NewDemoteVolumesTask(ctx))
		volumeTransitionStage.Tasks = append(volumeTransitionStage.Tasks, NewWaitForVolumesDemotedTask(ctx))
		volumeTransitionStage.Tasks = append(volumeTransitionStage.Tasks, NewMarkVolumesReadyForPromotionTask(ctx))
		workflow.Stages = append(workflow.Stages, volumeTransitionStage)
	}

	// Tasks that are executed only on target cluster
	if ctx.IsTargetCluster {
		// STAGE 4: VOLUME TRANSITION (TARGET)
		targetVolumeStage := &WorkflowStage{
			Name:        "VolumeTransitionTarget",
			Description: "CONSISTENCY MODE: Wait for source shutdown, then promote volumes in target cluster to Primary role",
			Tasks:       []WorkflowTask{},
		}

		// Add target volume tasks with more detailed descriptions for CONSISTENCY mode
		targetVolumeStage.Tasks = append(targetVolumeStage.Tasks, NewWaitForVolumesReadyForPromotionTask(ctx))
		targetVolumeStage.Tasks = append(targetVolumeStage.Tasks, NewPromoteVolumesTask(ctx))
		targetVolumeStage.Tasks = append(targetVolumeStage.Tasks, NewWaitForVolumesPromotedTask(ctx))
		targetVolumeStage.Tasks = append(targetVolumeStage.Tasks, NewVerifyDataAvailabilityTask(ctx))
		workflow.Stages = append(workflow.Stages, targetVolumeStage)

		// STAGE 5: TARGET CLUSTER ACTIVATION
		targetActivationStage := &WorkflowStage{
			Name:        "TargetClusterActivation",
			Description: "CONSISTENCY MODE: Activate target cluster only after volumes are successfully promoted",
			Tasks:       []WorkflowTask{},
		}

		// Add target activation tasks
		targetActivationStage.Tasks = append(targetActivationStage.Tasks, NewScaleUpWorkloadsTask(ctx))
		targetActivationStage.Tasks = append(targetActivationStage.Tasks, NewTriggerFluxReconciliationTask(ctx))
		targetActivationStage.Tasks = append(targetActivationStage.Tasks, NewWaitForTargetReadyTask(ctx))
		targetActivationStage.Tasks = append(targetActivationStage.Tasks, NewUpdateNetworkResourcesTask(ctx, "enable"))
		workflow.Stages = append(workflow.Stages, targetActivationStage)
	}

	// STAGE 6: COMPLETION
	completionStage := &WorkflowStage{
		Name:        "Completion",
		Description: "Complete the failover by updating global state and recording history",
		Tasks:       []WorkflowTask{},
	}

	// Add completion tasks
	completionStage.Tasks = append(completionStage.Tasks, NewUpdateGlobalStateTask(ctx))
	completionStage.Tasks = append(completionStage.Tasks, NewRecordFailoverHistoryTask(ctx))
	completionStage.Tasks = append(completionStage.Tasks, NewReleaseLockTask(ctx))
	workflow.Stages = append(workflow.Stages, completionStage)

	return workflow
}

// createUptimeModeWorkflow creates a workflow for UPTIME mode failover
func (f *Factory) createUptimeModeWorkflow(ctx *WorkflowContext) *Workflow {
	workflow := &Workflow{
		Name:        "UptimeModeFailover",
		Description: "Failover workflow that prioritizes service uptime",
		Context:     ctx,
		Stages:      []*WorkflowStage{},
	}

	// STAGE 1: INITIALIZATION
	initStage := &WorkflowStage{
		Name:        "Initialization",
		Description: "Prepare for failover by validating prerequisites and acquiring locks",
		Tasks:       []WorkflowTask{},
	}

	// Add initialization tasks
	initStage.Tasks = append(initStage.Tasks, NewValidateFailoverTask(ctx))
	initStage.Tasks = append(initStage.Tasks, NewAcquireLockTask(ctx))
	workflow.Stages = append(workflow.Stages, initStage)

	// Tasks that are executed only on target cluster
	if ctx.IsTargetCluster {
		// STAGE 2: TARGET CLUSTER PREPARATION
		targetPrepStage := &WorkflowStage{
			Name:        "TargetClusterPreparation",
			Description: "Prepare target cluster by promoting volumes and scaling up workloads",
			Tasks:       []WorkflowTask{},
		}

		// Add target preparation tasks
		targetPrepStage.Tasks = append(targetPrepStage.Tasks, NewPromoteVolumesTask(ctx))
		targetPrepStage.Tasks = append(targetPrepStage.Tasks, NewWaitForVolumesPromotedTask(ctx))
		targetPrepStage.Tasks = append(targetPrepStage.Tasks, NewVerifyDataAvailabilityTask(ctx))
		targetPrepStage.Tasks = append(targetPrepStage.Tasks, NewScaleUpWorkloadsTask(ctx))
		targetPrepStage.Tasks = append(targetPrepStage.Tasks, NewTriggerFluxReconciliationTask(ctx))
		targetPrepStage.Tasks = append(targetPrepStage.Tasks, NewWaitForTargetReadyTask(ctx))
		targetPrepStage.Tasks = append(targetPrepStage.Tasks, NewMarkTargetReadyForTrafficTask(ctx))
		workflow.Stages = append(workflow.Stages, targetPrepStage)

		// STAGE 3: TRAFFIC TRANSITION
		trafficTransitionStage := &WorkflowStage{
			Name:        "TrafficTransition",
			Description: "Switch traffic to the target cluster",
			Tasks:       []WorkflowTask{},
		}

		// Add traffic transition tasks
		trafficTransitionStage.Tasks = append(trafficTransitionStage.Tasks, NewWaitForTargetReadyForTrafficTask(ctx))
		trafficTransitionStage.Tasks = append(trafficTransitionStage.Tasks, NewUpdateNetworkResourcesTask(ctx, "enable"))
		trafficTransitionStage.Tasks = append(trafficTransitionStage.Tasks, NewMarkTrafficTransitionCompleteTask(ctx))
		workflow.Stages = append(workflow.Stages, trafficTransitionStage)
	}

	// Tasks that are executed only on source cluster
	if ctx.IsSourceCluster {
		// STAGE 4: SOURCE CLUSTER DEACTIVATION
		sourceDeactivationStage := &WorkflowStage{
			Name:        "SourceClusterDeactivation",
			Description: "Deactivate the source cluster after target is serving traffic",
			Tasks:       []WorkflowTask{},
		}

		// Add source deactivation tasks
		sourceDeactivationStage.Tasks = append(sourceDeactivationStage.Tasks, NewWaitForTrafficTransitionCompleteTask(ctx))
		sourceDeactivationStage.Tasks = append(sourceDeactivationStage.Tasks, NewUpdateNetworkResourcesTask(ctx, "disable"))
		sourceDeactivationStage.Tasks = append(sourceDeactivationStage.Tasks, NewScaleDownWorkloadsTask(ctx))
		sourceDeactivationStage.Tasks = append(sourceDeactivationStage.Tasks, NewDisableFluxReconciliationTask(ctx))
		sourceDeactivationStage.Tasks = append(sourceDeactivationStage.Tasks, NewWaitForWorkloadsScaledDownTask(ctx))
		sourceDeactivationStage.Tasks = append(sourceDeactivationStage.Tasks, NewDemoteVolumesTask(ctx))
		sourceDeactivationStage.Tasks = append(sourceDeactivationStage.Tasks, NewWaitForVolumesDemotedTask(ctx))
		workflow.Stages = append(workflow.Stages, sourceDeactivationStage)
	}

	// STAGE 5: COMPLETION
	completionStage := &WorkflowStage{
		Name:        "Completion",
		Description: "Complete the failover by updating global state and recording history",
		Tasks:       []WorkflowTask{},
	}

	// Add completion tasks
	completionStage.Tasks = append(completionStage.Tasks, NewUpdateGlobalStateTask(ctx))
	completionStage.Tasks = append(completionStage.Tasks, NewRecordFailoverHistoryTask(ctx))
	completionStage.Tasks = append(completionStage.Tasks, NewReleaseLockTask(ctx))
	workflow.Stages = append(workflow.Stages, completionStage)

	return workflow
}
