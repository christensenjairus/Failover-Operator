# Failover-Operator Architecture

This document describes the architecture of the Failover-Operator, which manages failover of VolumeReplications, workloads, Flux resources, and VirtualServices in Kubernetes.

## Directory Structure

```
internal/
├── controller/
│   ├── failoverpolicy_controller.go  # Main controller logic
│   ├── setup.go                      # Scheme registration
│   ├── volumereplication/            # Volume replication management
│   │   └── manager.go
│   ├── workload/                     # Workload management (Deployments, StatefulSets, CronJobs)
│   │   └── manager.go
│   ├── flux/                         # Flux resource management
│   │   └── manager.go
│   ├── virtualservice/               # Virtual service management
│   │   └── manager.go
│   └── status/                       # Status management
│       └── manager.go
```

## Operating Modes

The operator supports two distinct operating modes that control the reconciliation flow:

### Safe Mode (Default)

Safe mode (`mode: "on"` or not specified) prioritizes data integrity and application stability:

- Resources are processed in a carefully ordered sequence with wait states
- During secondary transition: Workloads are fully terminated before VolumeReplications change state
- During primary transition: Storage is fully available before workloads are deployed
- Sequential processing prevents race conditions and resource conflicts
- Detailed status verification ensures each step completes before proceeding

### Unsafe Mode

Unsafe mode (`mode: "off"`) prioritizes speed and bypasses the ordered reconciliation:

- All resources are processed in parallel without waiting for dependencies
- No verification that workloads are fully terminated before changing volume states
- No verification that volumes are primary before resuming Flux resources
- Faster but potentially riskier for data integrity
- Suitable for testing or non-critical environments

#### Future Enhancements

In a future release, unsafe mode will also enable cross-cluster coordination, where:
- The controller will signal to a remote cluster that it can promote its volumes immediately
- This will happen even before the current cluster has completed demoting its own volumes
- This will significantly reduce the total time required for site switchovers in multi-cluster environments

```go
// Simplified controller logic showing mode handling
isSafeMode := true  // Default to safe mode
if mode != "" {
    isSafeMode = strings.ToLower(mode) != "off"
}

if !isSafeMode {
    // Process all resources in parallel
    ProcessFluxResources(...)
    ProcessVirtualServices(...)
    ProcessWorkloads(...)
    ProcessVolumeReplications(...)
} else {
    // Process in ordered sequence with wait states
    if desiredState == "secondary" {
        // Suspend Flux → Update VirtualServices → Suspend CronJobs → 
        // Scale Down workloads → Wait → Update VolumeReplications
    } else {
        // Update VolumeReplications → Update VirtualServices → 
        // Resume CronJobs → Wait for volumes → Resume Flux
    }
}
```

## Component Responsibilities

### Main Controller (failoverpolicy_controller.go)

The main controller is responsible for:
- Coordinating the overall reconciliation process
- Initializing the component managers
- Orchestrating the sequencing of operations
- Ensuring all resources are processed in the correct order
- Implementing wait states for critical transitions
- Handling the scheduling of reconciliation loops

### VolumeReplication Manager (volumereplication/manager.go)

Responsible for:
- Updating VolumeReplication resources
- Checking and handling VolumeReplication errors
- Managing the transition between primary and secondary states
- Verifying VolumeReplications have reached their desired state
- Implementing safe mode transitions
- Detecting and handling transitional states and errors

### Workload Manager (workload/manager.go)

Responsible for:
- Managing Deployment, StatefulSet, and CronJob resources
- Scaling down workloads in secondary mode
- Verifying workloads are fully terminated (checking all status fields)
- Managing CronJob suspension
- Reporting detailed workload status

### Flux Manager (flux/manager.go)

Responsible for:
- Managing HelmRelease and Kustomization resources
- Suspending Flux resources in secondary mode
- Resuming Flux resources in primary mode
- Using unstructured resources to avoid direct Flux CRD dependencies

### VirtualService Manager (virtualservice/manager.go)

Responsible for:
- Managing VirtualService resources
- Updating VirtualService annotations based on failover state
- Controlling DNS resolution behavior during failovers
- Using unstructured resources to avoid direct Istio CRD dependencies

### Status Manager (status/manager.go)

Responsible for:
- Updating the FailoverPolicy status
- Managing FailoverPolicy conditions (Complete, InProgress, Error)
- Tracking the status of all managed resources
- Collecting and reporting error information
- Generating human-readable status summaries

## Detailed Reconciliation Flow

### Secondary Mode Reconciliation (Scaling Down)

```
┌───────────────────────┐
│ Fetch FailoverPolicy  │
└─────────────┬─────────┘
              │
              ▼
┌───────────────────────┐
│ Initialize Managers   │
└─────────────┬─────────┘
              │
              ▼
┌───────────────────────┐
│ Group Resources by    │
│       Type            │
└─────────────┬─────────┘
              │
              ▼
┌───────────────────────┐  No   ┌───────────────────────┐
│    Safe Mode?         │──────▶│ Process All Resources │
│                       │       │ In Parallel (Unsafe)  │
└─────────────┬─────────┘       └───────────────────────┘
              │ Yes
              ▼
┌───────────────────────┐     ┌──────────────────────┐
│ Suspend Flux          │     │ Process Flux         │
│ Resources First       │────▶│ HelmReleases &       │
│                       │     │ Kustomizations       │
└─────────────┬─────────┘     └──────────────────────┘
              │
              ▼
┌───────────────────────┐     ┌──────────────────────┐
│ Update VirtualServices│     │ Set External-DNS     │
│ Immediately           │────▶│ Annotation to        │
│                       │     │ "ignore"             │
└─────────────┬─────────┘     └──────────────────────┘
              │
              ▼
┌───────────────────────┐     ┌──────────────────────┐
│ Suspend CronJobs      │     │ Set CronJob.Spec.    │
│ Immediately           │────▶│ Suspend = true       │
│                       │     │                      │
└─────────────┬─────────┘     └──────────────────────┘
              │
              ▼
┌───────────────────────┐     ┌──────────────────────┐
│ Scale Down            │     │ Set Deployments &    │
│ Deployments &         │────▶│ StatefulSets         │
│ StatefulSets          │     │ Replicas = 0         │
└─────────────┬─────────┘     └──────────────────────┘
              │
              ▼
┌───────────────────────┐
│ Update Resource       │
│ Status                │
└─────────────┬─────────┘
              │
              ▼
┌───────────────────────┐  No  ┌──────────────────────┐
│ All Workloads Fully   │──────▶│ Requeue After 5s    │
│ Terminated?           │      │                      │
└─────────────┬─────────┘      └──────────────────────┘
              │ Yes
              ▼
┌───────────────────────┐     ┌──────────────────────┐
│ Process Volume        │     │ Set VolumeReplication│
│ Replications Last     │────▶│ to Secondary Mode    │
│                       │     │                      │
└─────────────┬─────────┘     └──────────────────────┘
              │
              ▼
┌───────────────────────┐
│ Update Final Status   │
└─────────────┬─────────┘
              │
              ▼
┌───────────────────────┐
│ Requeue After 20s     │
│ (Periodic Check)      │
└───────────────────────┘
```

### Primary Mode Reconciliation (Activating)

```
┌───────────────────────┐
│ Fetch FailoverPolicy  │
└─────────────┬─────────┘
              │
              ▼
┌───────────────────────┐
│ Initialize Managers   │
└─────────────┬─────────┘
              │
              ▼
┌───────────────────────┐
│ Group Resources by    │
│       Type            │
└─────────────┬─────────┘
              │
              ▼
┌───────────────────────┐  No   ┌───────────────────────┐
│    Safe Mode?         │──────▶│ Process All Resources │
│                       │       │ In Parallel (Unsafe)  │
└─────────────┬─────────┘       └───────────────────────┘
              │ Yes
              ▼
┌───────────────────────┐     ┌──────────────────────┐
│ Process Volume        │     │ Set VolumeReplication│
│ Replications First    │────▶│ to Primary Mode      │
│                       │     │                      │
└─────────────┬─────────┘     └──────────────────────┘
              │
              ▼
┌───────────────────────┐     ┌──────────────────────┐
│ Update VirtualServices│     │ Set External-DNS     │
│ Immediately           │────▶│ Annotation to        │
│                       │     │ "dns-controller"     │
└─────────────┬─────────┘     └──────────────────────┘
              │
              ▼
┌───────────────────────┐     ┌──────────────────────┐
│ Resume CronJobs       │     │ Set CronJob.Spec.    │
│ Immediately           │────▶│ Suspend = false      │
│                       │     │                      │
└─────────────┬─────────┘     └──────────────────────┘
              │
              ▼
┌───────────────────────┐
│ Update Resource       │
│ Status                │
└─────────────┬─────────┘
              │
              ▼
┌───────────────────────┐  No  ┌──────────────────────┐
│ All VolumeReplications│──────▶│ Requeue After 5s    │
│ Fully Primary?        │      │                      │
└─────────────┬─────────┘      └──────────────────────┘
              │ Yes
              ▼
┌───────────────────────┐     ┌──────────────────────┐
│ Resume Flux Resources │     │ Flux Will Handle     │
│ Last                  │────▶│ Scaling Up Workloads │
│                       │     │                      │
└─────────────┬─────────┘     └──────────────────────┘
              │
              ▼
┌───────────────────────┐
│ Update Final Status   │
└─────────────┬─────────┘
              │
              ▼
┌───────────────────────┐
│ Complete Reconciliation│
└───────────────────────┘
```

## Implementation Details

### Workload Status Verification

The workload manager verifies the complete termination of workloads by examining all status fields:

```go
// Check if fully scaled down - both spec and status must show 0 replicas
if deployment.Spec.Replicas != nil && *deployment.Spec.Replicas == 0 {
    // Check that all status replicas are also 0
    if deployment.Status.Replicas == 0 && 
       deployment.Status.AvailableReplicas == 0 && 
       deployment.Status.ReadyReplicas == 0 &&
       deployment.Status.UpdatedReplicas == 0 {
        status.State = "Scaled Down"
    } else {
        status.State = "Scaling Down"
        status.Error = fmt.Sprintf("Deployment is scaling down: %d/%d/%d/%d replicas (total/available/ready/updated)",
            deployment.Status.Replicas,
            deployment.Status.AvailableReplicas,
            deployment.Status.ReadyReplicas,
            deployment.Status.UpdatedReplicas)
    }
}
```

### VolumeReplication Status Verification

The VolumeReplication manager checks that all volumes have fully reached their desired state:

```go
// AreAllVolumesInDesiredState checks if all VolumeReplications have reached the desired state
func (m *Manager) AreAllVolumesInDesiredState(ctx context.Context, namespace string, vrNames []string, desiredState string) (bool, string) {
    for _, vrName := range vrNames {
        // Fetch the VolumeReplication object
        volumeReplication := &replicationv1alpha1.VolumeReplication{}
        err := m.client.Get(ctx, types.NamespacedName{Name: vrName, Namespace: namespace}, volumeReplication)
        
        // Get current status state 
        currentStatus := strings.ToLower(string(volumeReplication.Status.State))
        
        // Check if the status matches the desired state
        if currentStatus != strings.ToLower(desiredState) {
            // Not ready yet
            return false, fmt.Sprintf("VolumeReplication %s not in desired state", vrName)
        }
    }
    
    // All volumes are in desired state
    return true, ""
}
```

### Using Unstructured Resources for External CRDs

The operator uses `unstructured.Unstructured` to work with external CRDs (like Istio VirtualServices and Flux resources), avoiding direct dependencies:

```go
// Using unstructured approach for VirtualServices
virtualService := &unstructured.Unstructured{}
virtualService.SetAPIVersion("networking.istio.io/v1")
virtualService.SetKind("VirtualService")

// Fetch VirtualService object
err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, virtualService)
```

## Design Principles

The code follows these design principles:

1. **Separation of Concerns**: Each component has a clear, single responsibility
2. **Ordered Orchestration**: Operations execute in a carefully designed sequence
3. **Wait States**: Critical transitions include wait states to ensure safety 
4. **Status Verification**: The operator actively verifies resource status, not just configuration
5. **Dependency Injection**: Components receive their dependencies through constructors
6. **Interface-Based Design**: Components define interfaces for their dependencies, making testing easier
7. **Immutability**: Components don't modify their dependencies

## Common Patterns

1. **Manager Pattern**: Each subsystem has a manager that encapsulates its logic
2. **Context Propagation**: Context is passed through all function calls
3. **Explicit Error Handling**: Errors are explicitly checked and propagated
4. **Status Aggregation**: The controller aggregates status information from multiple sources 
5. **Resource Grouping**: Resources are grouped by type for efficient processing
6. **Conditional Reconciliation**: Reconciliation flow differs based on desired state
7. **Periodic Requeuing**: Regular checks ensure resources stay in desired state 