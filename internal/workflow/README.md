# Failover Operator Workflow System

This package implements a robust, task-based workflow system for orchestrating failover operations across Kubernetes clusters.

## Overview

The workflow system provides a structured approach to handle failover operations, with two main modes:

1. **Consistency Mode**: Prioritizes data consistency, shutting down the source before activating the target.
2. **Uptime Mode**: Prioritizes service availability, bringing up the target before deactivating the source.

## Architecture

The workflow system is built around the following components:

### Core Components

- **Engine**: Executes a sequence of tasks, handling dependency injection and error propagation.
- **Factory**: Creates workflow definitions with appropriate tasks based on failover mode.
- **Tasks**: Individual units of work implementing the `WorkflowTask` interface.
- **DependencyInjector**: Provides dependencies (Kubernetes client, logger, DynamoDB) to tasks.
- **ManagerAdapter**: Bridges between the failover controller and workflow system.

### Workflow Context

A shared context provides data that flows through the workflow, including:

- Failover and FailoverGroup resources
- Source and target cluster information
- Mode and operation parameters
- Metrics and timing data
- Lease token for DynamoDB locking

## Task Implementation

All tasks implement the `WorkflowTask` interface:

```go
type WorkflowTask interface {
    Execute(ctx context.Context) error
    GetName() string
    GetDescription() string
}
```

Each task:
1. Handles a specific part of the failover process
2. Returns an error if it fails
3. Has clear logging for observability
4. Maintains idempotency where possible

## Workflow Stages

Tasks are organized into logical stages:

1. **Initialization**: Validation and lock acquisition
2. **Source Preparation**: Scale down workloads, disable networking
3. **Volume Transition**: Demote source volumes, promote target volumes
4. **Target Activation**: Scale up workloads, enable networking
5. **Completion**: Update global state, record history, release locks

## Usage

The workflow system is designed to be used through the `ManagerAdapter`:

```go
adapter := workflow.NewManagerAdapter(client, logger, clusterName, dynamoDBService)
err := adapter.ProcessFailover(ctx, failoverObject, failoverGroupObject)
```

## Error Handling

Errors are propagated through the execution chain, with:
- Detailed error messages that include the failing task
- Appropriate logging at each step
- Status updates to the Failover resource

## Extending the System

To add new tasks:
1. Create a new task struct that embeds `BaseTask`
2. Implement the `Execute` method returning `error`
3. Add appropriate constructor
4. Update the GetBaseTask method in the injector
5. Add the task to the appropriate workflow in factory.go 