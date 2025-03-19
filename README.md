# Failover Operator

A Kubernetes operator for managing site failover with volume replication and workload management.

## Overview

The Failover Operator manages site failovers by:

1. Coordinating the failover of volume replications
2. Managing workloads (Deployments, StatefulSets, CronJobs)
3. Managing Flux resources (HelmReleases, Kustomizations)
4. Updating Istio VirtualServices

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
kind: FailoverPolicy
metadata:
  name: failoverpolicy-sample
  annotations:
    # Manage state via annotations (preferred)
    failover-operator.hahomelabs.com/desired-state: "PRIMARY"
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
      
```

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
kubectl apply -f config/crd/bases/crd.hahomelabs.com_failoverpolicies.yaml

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
