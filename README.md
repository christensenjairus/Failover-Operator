# Failover Operator

The Failover Operator is a Kubernetes operator that manages the failover process between multiple clusters. It coordinates the failover of various components, including:

- Volume Replications
- StatefulSets
- CronJobs
- Ingresses
- DynamoDB (for coordination)

## Project Structure

The project is organized as follows:

```
internal/
  controller/
    volumereplications/  # Manages volume replication
      manager.go         # Implementation
      manager_test.go    # Tests
    statefulsets/        # Manages StatefulSets
      manager.go         # Implementation
      manager_test.go    # Tests
    cronjobs/            # Manages CronJobs
      manager.go         # Implementation
      manager_test.go    # Tests
    ingresses/           # Manages Ingresses
      manager.go         # Implementation
      manager_test.go    # Tests
    dynamodb/            # Manages DynamoDB coordination
      manager.go         # Implementation
      manager_test.go    # Tests
```

## Component Managers

Each component manager is responsible for managing a specific type of resource during failover:

### VolumeReplications Manager

The VolumeReplications manager handles the replication state of volumes. During failover:
- The PRIMARY cluster's volumes are set to "primary" (read-write)
- The STANDBY cluster's volumes are set to "secondary" (read-only)

### StatefulSets Manager

The StatefulSets manager handles scaling StatefulSets. During failover:
- The PRIMARY cluster scales up StatefulSets to the desired replica count
- The STANDBY cluster scales down StatefulSets to 0 replicas

### CronJobs Manager

The CronJobs manager handles suspending and resuming CronJobs. During failover:
- The PRIMARY cluster resumes CronJobs to allow them to run
- The STANDBY cluster suspends CronJobs to prevent them from running

### Ingresses Manager

The Ingresses manager handles updating Ingress resources. During failover:
- The PRIMARY cluster enables DNS controller annotations
- The STANDBY cluster disables DNS controller annotations

### DynamoDB Manager

The DynamoDB manager handles coordination between clusters. It:
- Tracks which cluster is PRIMARY for each FailoverGroup
- Provides distributed locking to prevent concurrent failovers
- Records failover history for auditing
- Tracks operator heartbeats to detect when a cluster is unhealthy

## Implementation Guide

This codebase currently contains stubs for all the required functionality. To implement the actual functionality:

1. Start with the DynamoDB manager to implement coordination features:
   - Implement the AWS SDK client integration
   - Implement the record operations (GetOwnership, UpdateOwnership, etc.)
   - Implement the locking operations (AcquireLock, ReleaseLock)
   - Implement the heartbeat operations (UpdateHeartbeat, GetHeartbeats)

2. Implement the individual component managers:
   - VolumeReplications: Implement the CR interactions using the unstructured client
   - StatefulSets: Implement the scaling operations
   - CronJobs: Implement the suspend/resume operations
   - Ingresses: Implement the annotation and DNS operations

3. Update the tests to use actual mock expectations instead of placeholder assertions

4. Implement the controller that uses these managers to coordinate failover

## Running the Tests

The tests are currently placeholders and will pass even without implementation. Once you implement the actual functionality, you'll need to update the tests to properly verify the behavior.

```bash
go test ./internal/controller/...
```

## Linter Errors

There may be linter errors in the test files due to missing imports and unimplemented functions. These will be resolved once the actual implementations are completed.

## Failover Process

The failover process will work as follows:

1. An operator instance detects that a PRIMARY cluster is unhealthy (no heartbeats)
2. The operator acquires a lock for the FailoverGroup
3. The operator updates the ownership record to designate a new PRIMARY cluster
4. The operator in the new PRIMARY cluster:
   - Sets its VolumeReplications to "primary"
   - Scales up its StatefulSets
   - Resumes its CronJobs
   - Enables DNS controller annotations on its Ingresses
5. The operator in the old PRIMARY cluster (when it recovers):
   - Sets its VolumeReplications to "secondary"
   - Scales down its StatefulSets
   - Suspends its CronJobs
   - Disables DNS controller annotations on its Ingresses
6. The lock is released
7. The failover event is recorded in history

## Next Steps

1. Implement the AWS SDK client integration for DynamoDB
2. Complete the implementation of each manager
3. Develop the controller that uses these managers
4. Test the failover process end-to-end

## Overview

The Failover Operator manages site failovers by:

1. Coordinating the failover of volume replications
2. Managing workloads (Deployments, StatefulSets, CronJobs)
3. Managing Flux resources (HelmReleases, Kustomizations)
4. Updating Istio VirtualServices

## Custom Resource Definitions

The operator uses two Custom Resource Definitions (CRDs) to define and manage failover operations:

### 1. FailoverGroup CRD

The `FailoverGroup` resource defines a group of components that need to be failed over together, along with their desired configurations.

Example:

```yaml
apiVersion: crd.hahomelabs.com/v1alpha1
kind: FailoverGroup
metadata:
  name: application-db-group
spec:
  # Default failover mode for all components (if not specified per component)
  # Options: "safe" or "fast"
  defaultFailoverMode: safe
  
  # Define components within this failover group
  components:
    - name: database
      # Override the default failover mode for this component
      failoverMode: fast
      # Workloads to manage
      workloads:
        - kind: StatefulSet
          name: mysql
      # Volume replications to manage
      volumeReplications:
        - mysql-data-volume
    
    - name: api
      workloads:
        - kind: Deployment
          name: api-server
        - kind: Deployment
          name: api-worker
      volumeReplications:
        - api-config-volume
      # Virtual services to manage
      virtualServices:
        - api-virtualservice
status:
  # Current state (PRIMARY or STANDBY)
  state: PRIMARY
  # Overall health status (OK, DEGRADED, ERROR)
  health: OK
  # Component-level status
  components:
    - name: database
      health: OK
    - name: api
      health: OK
```

### 2. Failover CRD

The `Failover` resource defines an actual failover operation to be performed against one or more `FailoverGroup` resources.

Example:

```yaml
apiVersion: crd.hahomelabs.com/v1alpha1
kind: Failover
metadata:
  name: site-failover
spec:
  # Type of failover (planned or emergency)
  type: planned
  # Target cluster to make PRIMARY
  targetCluster: cluster-east
  # FailoverGroups to include in this failover operation
  failoverGroups:
    - name: application-db-group
      namespace: default
    - name: frontend-group
      namespace: web
status:
  # Overall status of the failover (IN_PROGRESS, SUCCESS, FAILED)
  status: SUCCESS
  # Metrics about the failover operation
  metrics:
    totalFailoverTimeSeconds: 143
  # Status of each failover group involved
  failoverGroups:
    - name: application-db-group
      namespace: default
      status: SUCCESS
      startTime: "2023-06-15T05:21:34Z"
      completionTime: "2023-06-15T05:23:12Z"
    - name: frontend-group
      namespace: web
      status: SUCCESS
      startTime: "2023-06-15T05:21:34Z"
      completionTime: "2023-06-15T05:23:57Z"
  # Detailed conditions
  conditions:
    - type: Started
      status: "True"
      lastTransitionTime: "2023-06-15T05:21:34Z"
      reason: FailoverStarted
      message: "Failover operation has started"
    - type: Completed
      status: "True"
      lastTransitionTime: "2023-06-15T05:23:57Z"
      reason: FailoverCompleted
      message: "Failover operation completed successfully"
```

## Detailed Functionality

### Orchestration Flow

The operator implements a sophisticated, ordered reconciliation process to ensure safe and reliable failover between sites:

#### Secondary Mode (Scaling Down a Site)

When transitioning to `secondary` mode, operations happen in this sequence:

1. **Suspend Flux Resources** - First, HelmReleases and Kustomizations are suspended to prevent Flux from fighting against the operator's actions
2. **Update VirtualServices** - Traffic is immediately redirected away from this site
3. **Suspend CronJobs** - All CronJobs are immediately suspended to prevent new Jobs from being created
4. **Scale Down Deployments/StatefulSets** - Workloads are scaled to 0 replicas
5. **Wait for Workload Termination** - Critically, the operator waits until all pods are fully terminated (not just scheduled for termination)
6. **Update VolumeReplications** - Only after all workloads are confirmed to be fully terminated, volume replications are set to `secondary` mode

This sequence ensures that applications can gracefully shut down and flush any data to storage before the volumes become read-only in secondary mode.

```
┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐
│  Suspend Flux   │────▶│ Update Virtual  │────▶│ Suspend CronJobs│
│   Resources     │     │   Services      │     │                 │
└─────────────────┘     └─────────────────┘     └─────────────────┘
          │                                                │
          ▼                                                ▼
┌─────────────────┐                             ┌─────────────────┐
│ Scale Down      │                             │   WAIT until    │
│ Deployments/    │◀────────────────────────────│  Workloads are  │
│ StatefulSets    │                             │ Fully Terminated│
└─────────────────┘                             └─────────────────┘
                                                        │
                                                        ▼
                                                ┌─────────────────┐
                                                │ Update Volume   │
                                                │ Replications to │
                                                │ Secondary Mode  │
                                                └─────────────────┘
```

#### Primary Mode (Activating a Site)

When transitioning to `primary` mode, operations happen in this sequence:

1. **Update VolumeReplications** - First, volume replications are set to `primary` mode
2. **Update VirtualServices** - Traffic is immediately directed to this site
3. **Resume CronJobs** - CronJobs are unsuspended to allow scheduled tasks to run
4. **Wait for VolumeReplications** - The operator waits until all VolumeReplications have fully reached the `primary` state
5. **Resume Flux Resources** - Only after all volumes are confirmed to be in primary mode, Flux resources are resumed
   (Flux will then automatically manage scaling up the workloads)

This sequence ensures that storage is fully ready and accessible before Flux begins deploying workloads that depend on that storage.

```
┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐
│ Update Volume   │────▶│ Update Virtual  │────▶│ Resume CronJobs │
│ Replications to │     │   Services      │     │                 │
│ Primary Mode    │     └─────────────────┘     └─────────────────┘
└─────────────────┘                                      │
          │                                              ▼
          │                                    ┌─────────────────┐
          │                                    │   WAIT until    │
          │                                    │ VolumeReplication│
          │                                    │ Fully Primary   │
          │                                    └─────────────────┘
          │                                              │
          ▼                                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                 Resume Flux Resources                           │
│        (Flux automatically scales up workloads)                 │
└─────────────────────────────────────────────────────────────────┘
```

### Key Features

#### Workload Status Verification

When scaling down workloads, the operator doesn't merely set `replicas: 0` - it actively monitors:
- `deployment.status.replicas`
- `deployment.status.availableReplicas`
- `deployment.status.readyReplicas`
- `deployment.status.updatedReplicas`

Only when ALL of these status metrics reach zero is a workload considered fully terminated. This ensures applications have completely shut down before marking volumes as read-only, preventing data loss.

#### VolumeReplication Status Verification

Before resuming Flux in primary mode, the operator verifies that ALL VolumeReplications:
1. Have reached the `primary` state in their `.status.state` field
2. Do not have any error conditions

This ensures storage is fully ready before workloads are deployed that depend on that storage.

#### Support for External-DNS Integration

The operator manages VirtualService annotations that integrate with external-dns:
- In `primary` mode: Sets `external-dns.alpha.kubernetes.io/controller: "dns-controller"`
- In `secondary` mode: Sets `external-dns.alpha.kubernetes.io/controller: "ignore"`

This automatically updates DNS records to point to the active site.

## Custom Resource Definition

The operator uses a Custom Resource Definition (CRD) called `FailoverPolicy` to define the resources and behavior during failover.

Example:

```yaml
apiVersion: crd.hahomelabs.com/v1alpha1
kind: FailoverGroup
metadata:
  name: failovergroup-sample
spec:
  # Determine the failover approach - "safe" or "fast"
  failoverMode: safe
  
  # Define application components and their resources
  components:
    - name: database
      workloads:
        - kind: StatefulSet
          name: mysql
      volumeReplications:
        - mysql-data-volume
    
    - name: api
      workloads:
        - kind: Deployment
          name: api-server
        - kind: Deployment
          name: api-worker
      volumeReplications:
        - api-config-volume

    - name: frontend
      workloads:
        - kind: Deployment
          name: web-ui
      virtualServices:
        - web-virtualservice

  # Define Flux resources that need to be suspended during failover
  parentFluxResources:
    - kind: HelmRelease
      name: ingress-nginx
    - kind: Kustomization
      name: core-services
      
### Default State Behavior

When a new `FailoverGroup` is created, it is automatically initialized in the `PRIMARY` state. This design decision ensures that:

1. Flux can immediately begin reconciling resources without interference
2. Workloads can be deployed without manual intervention
3. The system starts in a fully operational state
4. No transition is required to begin serving traffic

This behavior is particularly important in GitOps workflows, where Flux or similar tools manage the application lifecycle. By defaulting to the `PRIMARY` state, the operator avoids blocking initial deployments.

```

## Automatic Failover Recovery

The Failover Operator includes a sophisticated recovery mechanism to automatically handle situations where failover operations fail or become deadlocked.

### Key Features

1. **Automatic Detection**: The operator automatically detects when a failover operation has:
   - Exceeded maximum allowed duration (30 minutes by default)
   - Has too many failed components (more than 50% of FailoverGroups)
   - Detected deadlocked workloads that are stuck in a transition state

2. **Automatic Recovery**: When a failing failover is detected, the operator:
   - Creates a recovery failover operation that targets the opposite direction
   - Marks the original failover as failed
   - Includes annotations to track the reason for recovery

3. **Recovery Failover Type**: The operator supports a special `recovery` failover type:
   ```yaml
   apiVersion: crd.hahomelabs.com/v1alpha1
   kind: Failover
   spec:
     # Special type for recovery operations
     type: recovery
     targetCluster: original-cluster
     failoverGroups:
       - name: application-db-group
         namespace: default
   ```

4. **Special Handling for Recovery Operations**: Recovery failovers use more aggressive tactics:
   - Skip usual waiting periods for workload readiness
   - Force immediate state transitions to break deadlocks
   - Prioritize system stability over data consistency
   - Apply all recovery actions in parallel for faster resolution

### Recovery Process Flow

When a failover operation is detected as failed:

1. The original failover is marked as `FAILED` with a detailed reason
2. A new recovery failover is created targeting the original cluster
3. The recovery failover uses aggressive tactics to break deadlocks:
   - For PRIMARY clusters: Forces immediate transition to STANDBY state
   - For STANDBY clusters: Forces immediate transition to PRIMARY state
4. The recovery is tracked with special conditions in the status field

### Deadlock Detection

The operator uses sophisticated detection for workloads stuck in transition:

- Monitors workload state changes over time
- Detects when replicas are stuck between desired and actual counts
- Considers a workload deadlocked when a transition hasn't changed in 15+ minutes
- Records detailed metrics about deadlocks for troubleshooting

### Component-Based Architecture

The operator now supports a component-based approach for defining managed resources:

- **Components**: Logical groupings of related resources that make up your application
- **Workloads**: Deployments, StatefulSets, and CronJobs within each component
- **VolumeReplications**: Storage resources associated with each component
- **VirtualServices**: Network routing resources associated with each component

This structure provides:
1. Better organization and grouping of related resources
2. More granular health checking at the component level
3. Clearer status reporting in the FailoverPolicy status

### Health Status Monitoring

The operator performs detailed health checks for each component and reports status at both the component and overall policy levels:

- **Overall Health**: Aggregated health status of all components
- **Component Health**: Individual health status for each defined component
- **Detailed Messages**: Human-readable messages explaining any issues

Health status values:
- `OK`: Everything is working as expected
- `DEGRADED`: Some resources are not in their desired state but the system is still functional
- `ERROR`: Critical failures have been detected

### FailoverPolicy Status Information

The operator updates the `status` field of the FailoverPolicy resource with detailed information:

```yaml
# This is an example of the output from the status fields
status:
  # Overall policy state
  state: PRIMARY
  health: OK
  
  # Component-level health status
  components:
    - name: database
      health: OK
    - name: api
      health: OK
    - name: frontend
      health: OK
  
  # Transition information
  lastTransitionMessage: Successfully transitioned to PRIMARY mode
  lastTransitionReason: Complete
  lastTransitionTime: "2023-06-15T05:21:34Z"
  
  # Volume replication status details
  volumeReplicationStatuses:
  - name: mysql-data-volume
    state: primary-rwx
    lastUpdateTime: "2023-06-15T05:21:34Z"
  - name: api-config-volume
    state: primary-rwx
    lastUpdateTime: "2023-06-15T05:21:34Z"
```

When issues are detected, the status will show more detailed information:

```yaml
status:
  state: PRIMARY
  health: DEGRADED
  
  components:
    - name: database
      health: OK
    - name: api
      health: DEGRADED
      message: "Deployment api-worker has 1/3 ready replicas; Container api-cache in pod api-worker-78d9bd6c4-2xvf4 is not ready"
    - name: frontend
      health: OK
  
  lastTransitionMessage: Successfully transitioned to PRIMARY mode
  lastTransitionReason: Complete
  lastTransitionTime: "2023-06-15T05:21:34Z"
  
  volumeReplicationStatuses:
  - name: mysql-data-volume
    state: primary-rwx
    lastUpdateTime: "2023-06-15T05:21:34Z"
  - name: api-config-volume
    state: primary-rwx
    lastUpdateTime: "2023-06-15T05:21:34Z"
```

## Failover Modes

The operator supports two modes:

### Safe Mode (Default)

In safe mode (`mode: "on"` or not specified):
- Resources are processed in a carefully ordered sequence
- Workloads are fully terminated before VolumeReplications are set to secondary
- VolumeReplications must reach primary state before Flux resources are resumed
- Additional validations occur during transitions
- More conservative error handling

This is the recommended mode for production environments where data integrity is critical.

### Unsafe Mode

In unsafe mode (`mode: "off"`):
- All resources are processed in parallel without waiting for ordered completion
- No waiting for workloads to fully terminate before marking volumes as secondary
- No waiting for volumes to be fully primary before resuming Flux resources
- Faster transitions with fewer validations and safety checks
- Suitable for testing, development, or non-critical environments

Unsafe mode trades safety for speed, completing failovers faster but potentially risking data integrity if applications haven't fully shut down before volumes become read-only.

> **Future Enhancement**: In a future release, unsafe mode will also signal to a remote cluster that it can promote its volumes as quickly as possible, even before the current cluster has demoted its volumes. This will enable even faster site switchovers in multi-cluster scenarios.

```yaml
apiVersion: crd.hahomelabs.com/v1alpha1
kind: FailoverPolicy
metadata:
  name: fast-failover-policy
spec:
  desiredState: secondary
  # Set to "off" to bypass ordered sequencing for faster operation
  mode: "off"
  managedResources:
    # resources...
```

## Installation

```bash
# Apply the CRD
kubectl apply -f config/crd/bases/crd.hahomelabs.com_failovergroups.yaml
kubectl apply -f config/crd/bases/crd.hahomelabs.com_failovers.yaml

# Deploy the operator
make deploy
```

## Development

```bash
# Generate manifests and code
make generate manifests

# Build the operator
make build

# Run the operator locally
make run
```

## Description

This Kubernetes operator allows you to define policies that manage the failover of applications between clusters using VolumeReplication resources and VirtualService configurations. It handles:

- Coordinating the state change of storage replications (primary/secondary)
- Managing DNS resolution through VirtualService annotations
- Monitoring the status of failover operations
- Supporting both regular and "safe" modes for different failover scenarios

For details about the code organization and architecture, see [ARCHITECTURE.md](ARCHITECTURE.md).

## Getting Started

### Prerequisites
- go version v1.22.0+
- docker version 17.03+.
- kubectl version v1.11.3+.
- Access to a Kubernetes v1.11.3+ cluster.

### To Deploy on the cluster
**Build and push your image to the location specified by `IMG`:**

```sh
make docker-build docker-push IMG=<some-registry>/failover-operator:tag
```

**NOTE:** This image ought to be published in the personal registry you specified.
And it is required to have access to pull the image from the working environment.
Make sure you have the proper permission to the registry if the above commands don't work.

**Install the CRDs into the cluster:**

```sh
make install
```

**Deploy the Manager to the cluster with the image specified by `IMG`:**

```sh
make deploy IMG=<some-registry>/failover-operator:tag
```

> **NOTE**: If you encounter RBAC errors, you may need to grant yourself cluster-admin
privileges or be logged in as admin.

**Create instances of your solution**
You can apply the samples (examples) from the config/sample:

```sh
kubectl apply -k config/samples/
```

>**NOTE**: Ensure that the samples has default values to test it out.

### To Uninstall
**Delete the instances (CRs) from the cluster:**

```sh
kubectl delete -k config/samples/
```

**Delete the APIs(CRDs) from the cluster:**

```sh
make uninstall
```

**UnDeploy the controller from the cluster:**

```sh
make undeploy
```

## Project Distribution

Following are the steps to build the installer and distribute this project to users.

1. Build the installer for the image built and published in the registry:

```
## Status

The operator maintains the status of the FailoverPolicy, including:

- Current state (active or passive)
- Health status
- Last transition details

Example output from status fields:

```yaml
status:
  state: PRIMARY
  health: healthy
  lastTransition:
    error: ""
    timestamp: "2023-06-01T12:30:45Z"
    transitionSuccessful: true
```

This shows a FailoverPolicy in active mode with a healthy status and successful last transition.
