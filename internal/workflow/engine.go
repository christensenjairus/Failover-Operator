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

	"github.com/go-logr/logr"
)

// TaskLogger defines the logging interface for tasks
type TaskLogger interface {
	Info(msg string, keysAndValues ...interface{})
	Error(err error, msg string, keysAndValues ...interface{})
}

// Task interface defines methods that all tasks must implement
type Task interface {
	GetName() string
	GetDescription() string
	Execute(ctx context.Context) error
}

// TaskExecutor executes tasks in a workflow
type TaskExecutor struct {
	Logger TaskLogger
}

// NewTaskExecutor creates a new task executor
func NewTaskExecutor(logger TaskLogger) *TaskExecutor {
	return &TaskExecutor{
		Logger: logger,
	}
}

// ExecuteTask executes a task and returns the result
func (e *TaskExecutor) ExecuteTask(ctx context.Context, task Task) error {
	e.Logger.Info("Executing task", "taskName", task.GetName())

	startTime := time.Now()
	err := task.Execute(ctx)
	duration := time.Since(startTime)

	if err != nil {
		e.Logger.Error(err, "Task execution failed",
			"taskName", task.GetName(),
			"durationMs", duration.Milliseconds())
		return fmt.Errorf("task '%s' failed: %w", task.GetName(), err)
	}

	e.Logger.Info("Task completed successfully",
		"taskName", task.GetName(),
		"durationMs", duration.Milliseconds())

	return nil
}

// Engine executes a workflow of tasks
type Engine struct {
	Tasks        []Task
	Executor     *TaskExecutor
	Context      *WorkflowContext
	DepsInjector DependencyInjector
}

// NewEngine creates a new workflow engine
func NewEngine(tasks []Task, logger logr.Logger, deps DependencyInjector) *Engine {
	return &Engine{
		Tasks:        tasks,
		Executor:     NewTaskExecutor(logger),
		DepsInjector: deps,
	}
}

// Execute runs the workflow engine
func (e *Engine) Execute(ctx context.Context, workflowCtx *WorkflowContext) error {
	e.Context = workflowCtx
	startTime := time.Now()

	e.Executor.Logger.Info("Starting workflow execution",
		"taskCount", len(e.Tasks),
		"targetCluster", workflowCtx.TargetClusterName,
		"mode", workflowCtx.FailoverMode)

	// Inject dependencies into all tasks
	for i := range e.Tasks {
		if e.DepsInjector != nil {
			// Type assertion to convert Task interface to WorkflowTask
			if workflowTask, ok := e.Tasks[i].(WorkflowTask); ok {
				if err := e.DepsInjector.InjectDependencies(workflowTask); err != nil {
					e.Executor.Logger.Error(err, "Failed to inject dependencies", "taskName", e.Tasks[i].GetName())
					return fmt.Errorf("failed to inject dependencies for task '%s': %w", e.Tasks[i].GetName(), err)
				}
			} else {
				e.Executor.Logger.Info("Task doesn't support dependency injection", "taskName", e.Tasks[i].GetName())
			}
		}
	}

	// Execute each task in sequence
	for i, task := range e.Tasks {
		e.Executor.Logger.Info("Executing workflow task",
			"taskIndex", i+1,
			"taskTotal", len(e.Tasks),
			"taskName", task.GetName())

		if err := e.Executor.ExecuteTask(ctx, task); err != nil {
			executionDuration := time.Since(startTime)
			e.Executor.Logger.Error(err, "Workflow execution failed",
				"durationMs", executionDuration.Milliseconds(),
				"failedTaskIndex", i+1,
				"failedTaskName", task.GetName())

			return fmt.Errorf("workflow execution failed at task %d (%s): %w",
				i+1, task.GetName(), err)
		}
	}

	executionDuration := time.Since(startTime)
	e.Executor.Logger.Info("Workflow execution completed successfully",
		"durationMs", executionDuration.Milliseconds(),
		"taskCount", len(e.Tasks))

	return nil
}

// ExecuteAsync executes the workflow asynchronously and sends the result to the provided channel
func (e *Engine) ExecuteAsync(ctx context.Context, workflowCtx *WorkflowContext, completionCh chan<- error) {
	go func() {
		completionCh <- e.Execute(ctx, workflowCtx)
	}()
}
