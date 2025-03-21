# Failover Controller Implementation Guide

This README provides guidance on the structured failover process implemented in the Failover Operator. It explains the architecture, code organization, and how the various stages of the failover process are implemented.

## Overview

The Failover Operator manages the process of switching workloads and data from one Kubernetes cluster (the source) to another (the target). This is a complex operation that requires coordination of multiple resources across clusters, including:

- Workloads (Deployments, StatefulSets, CronJobs)
- Network resources (VirtualServices, Ingresses)
- Persistent volumes and their replication
- GitOps resources (Flux)
- Global state in DynamoDB

## Failover Process Stages

The failover process is organized into six distinct stages, each with specific responsibilities:

1. **Initialization Stage** - Validate and prepare for failover
2. **Source Cluster Preparation Stage** - Prepare the source cluster for shutdown
3. **Volume Demotion Stage** - Demote volumes in source cluster
4. **Volume Promotion Stage** - Promote volumes in target cluster
5. **Target Cluster Activation Stage** - Activate workloads in target cluster
6. **Completion Stage** - Update global state and finalize

For detailed documentation of each stage, refer to `failover_stages.go`.

## Code Organization

The failover implementation is organized into the following files:

- `manager.go` - Core manager implementation with the main failover workflow
- `failover_controller.go` - Kubernetes controller reconciliation loop
- `auto_failover.go` - Logic for automatic failover detection and triggering
- `failover_stages.go` - Documentation of all failover stages
- `manager_test.go` - Tests for the manager implementation

## Implementation Details

### Manager Structure

The `Manager` struct in `manager.go` is the central component handling failover operations. It contains:

- Kubernetes client for interacting with cluster resources
- Resource managers for various resource types
- DynamoDB manager for global state
- Utility methods for each stage of the failover process

### Failover Execution Flow

The failover execution is implemented in the `executeFailoverWorkflow` method in `manager.go`. This method orchestrates all stages of the failover process and handles errors appropriately.

### Cluster Coordination

A critical aspect of the failover process is coordination between the source and target clusters:

1. The source cluster operator handles stages 1-3
2. The target cluster operator handles stages 4-5
3. Both communicate through DynamoDB global state

### Error Handling

Error handling follows a consistent pattern throughout the implementation:

- Contextual errors with stage information
- Status updates for observability
- Lock cleanup to prevent deadlocks
- History recording for auditing

## Development Guidelines

When making changes to the failover implementation:

1. **Respect stage boundaries** - Each stage has specific responsibilities
2. **Maintain idempotency** - Operations should be safely retryable
3. **Add appropriate logging** - Include contextual information in logs
4. **Update tests** - Each change should be covered by tests
5. **Document recovery procedures** - For complex failure modes

## Future Improvements

Planned improvements to the failover implementation:

1. Enhanced metrics collection during the failover process
2. More granular status reporting for complex operations
3. Progressive rollout options for large-scale applications
4. Circuit breaker patterns to prevent cascading failures

## References

- [Kubernetes Controller Runtime](https://github.com/kubernetes-sigs/controller-runtime)
- [CSI Volume Replication](https://github.com/csi-addons/volume-replication-operator)
- [Flux GitOps](https://fluxcd.io/docs/) 