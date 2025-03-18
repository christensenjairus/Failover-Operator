# Failover-Operator Architecture

This document describes the architecture of the Failover-Operator, which manages failover of VolumeReplications and VirtualServices in Kubernetes.

## Directory Structure

```
internal/
├── controller/
│   ├── failoverpolicy_controller.go  # Main controller logic
│   ├── setup.go                      # Scheme registration
│   ├── volumereplication/            # Volume replication management
│   │   └── manager.go
│   ├── virtualservice/               # Virtual service management
│   │   └── manager.go
│   └── status/                       # Status management
│       └── manager.go
```

## Component Responsibilities

### Main Controller (failoverpolicy_controller.go)

The main controller is responsible for:
- Coordinating the overall reconciliation process
- Initializing the component managers
- Handling the scheduling of reconciliation loops

### VolumeReplication Manager (volumereplication/manager.go)

Responsible for:
- Updating VolumeReplication resources
- Checking and handling VolumeReplication errors
- Managing the transition between primary and secondary states
- Implementing safe mode transitions

### VirtualService Manager (virtualservice/manager.go)

Responsible for:
- Managing VirtualService resources
- Updating VirtualService annotations based on failover state
- Controlling DNS resolution behavior during failovers

### Status Manager (status/manager.go)

Responsible for:
- Updating the FailoverPolicy status
- Managing FailoverPolicy conditions (Complete, InProgress, Error)
- Tracking problematic VolumeReplications

## Reconciliation Flow

1. The controller fetches the FailoverPolicy resource
2. It initializes the component managers
3. The VolumeReplication manager processes all volume replications
4. The VirtualService manager updates all virtual services
5. The Status manager updates the FailoverPolicy status
6. Based on pending updates or errors, the controller decides whether to requeue the request

## Design Principles

The code follows these design principles:

1. **Separation of Concerns**: Each component has a clear, single responsibility
2. **Dependency Injection**: Components receive their dependencies through constructors
3. **Interface-Based Design**: Components define interfaces for their dependencies, making testing easier
4. **Immutability**: Components don't modify their dependencies

## Common Patterns

1. **Manager Pattern**: Each subsystem has a manager that encapsulates its logic
2. **Context Propagation**: Context is passed through all function calls
3. **Explicit Error Handling**: Errors are explicitly checked and propagated
4. **Status Aggregation**: The controller aggregates status information from multiple sources 