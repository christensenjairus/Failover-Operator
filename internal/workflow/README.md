# Failover Operator Workflow System

This package implements a robust, task-based workflow system for orchestrating failover operations across Kubernetes clusters.

## Overview

The workflow system provides a structured approach to handle failover operations, with two main modes:

1. **Consistency Mode**: Prioritizes data consistency, shutting down the source before activating the target.
   - Ensures data integrity by first stopping application access to source 
   - Demotes volumes on source cluster before promoting on target
   - Creates a clean handoff with minimal risk of split-brain scenarios
   - Higher downtime but guarantees consistent state
   - Best for workloads where data integrity is critical (databases, financial systems)

2. **Uptime Mode**: Prioritizes service availability, bringing up the target before deactivating the source.
   - Minimizes service disruption by activating target first
   - Some risk of temporary inconsistency during transition
   - Lower downtime but has potential for split-brain issues
   - Best for stateless or read-heavy workloads

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

## CONSISTENCY Mode Workflow Stages

The CONSISTENCY mode workflow follows these specific stages:

1. **Initialization**
   - Validates failover prerequisites and safety checks
   - Acquires distributed lock in DynamoDB
   - Ensures all clusters have consistent view of the operation

2. **Source Cluster Shutdown** (Source Only)
   - Disables network resources (VirtualServices/Ingresses) to stop new traffic
   - Scales down workloads to gracefully terminate instances
   - Disables Flux GitOps reconciliation to prevent automated restarts
   - Waits for all workloads to fully scale down

3. **Volume Transition (Source)**
   - Demotes volumes from Primary to Secondary role
   - Waits for volumes to complete demoting to ensure no active writers
   - Marks volumes as ready for promotion in DynamoDB
   - This signals to target that it's safe to promote volumes

4. **Volume Transition (Target)**
   - Waits for volumes to be marked ready for promotion in DynamoDB
   - Promotes volumes from Secondary to Primary role
   - Verifies data availability and integrity
   - Ensures all persistent data is accessible before proceeding

5. **Target Cluster Activation**
   - Scales up workloads on target cluster
   - Triggers Flux GitOps reconciliation if needed
   - Waits for all workloads to be fully ready
   - Enables network resources to start receiving traffic

6. **Completion**
   - Updates global state in DynamoDB
   - Records failover metrics and history
   - Releases distributed lock
   - Updates FailoverGroup status

## Data Consistency Guarantees

The CONSISTENCY mode workflow provides the following guarantees:

1. **No Simultaneous Writers**: At no point will both clusters have Primary role for volumes
2. **Graceful Shutdown**: Applications are stopped before volume role changes
3. **Handoff Protocol**: Clear signal mechanism between clusters via DynamoDB
4. **Verification**: Data availability is verified before service restoration
5. **Atomic Promotion**: Coordinated promotion of all volumes in a group

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