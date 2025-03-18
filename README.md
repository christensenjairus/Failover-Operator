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
2. Are not in a transitioning state like `resync`
3. Do not have any error conditions

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
spec:
  # Determine the intended state - "primary" or "secondary"
  desiredState: primary
  
  # Determine the failover approach - "safe" or "unsafe"
  mode: safe
  
  # Define all managed resources in a single list
  managedResources:
    # Volume replications
    - kind: VolumeReplication
      name: volume-replication-1
    - kind: VolumeReplication
      name: volume-replication-2
      namespace: different-namespace
    
    # Kubernetes workloads
    - kind: Deployment
      name: web-frontend
    - kind: Deployment
      name: api-server
      namespace: api
    - kind: StatefulSet
      name: database
    - kind: CronJob
      name: backup-job
    
    # Flux resources
    - kind: HelmRelease
      name: ingress-nginx
      namespace: ingress-nginx
    - kind: Kustomization
      name: core-services
      namespace: flux-system
    
    # Istio resources
    - kind: VirtualService
      name: web-virtualservice
```

### Resource Management Behavior

When a `FailoverPolicy` is in **primary** mode:
- VolumeReplications are set to `primary` mode
- Deployments, StatefulSets, CronJobs are managed by Flux
- Flux resources (HelmReleases, Kustomizations) are active
- VirtualServices are updated to route traffic to this site

When a `FailoverPolicy` is in **secondary** mode:
- Flux resources are suspended first
- VirtualServices are immediately updated to stop routing traffic
- CronJobs are suspended 
- Deployments and StatefulSets are scaled down to 0 replicas
- System waits for all pods to be fully terminated
- VolumeReplications are then set to `secondary` mode

### FailoverPolicy Status Information

The operator updates the `status` field of the FailoverPolicy resource with detailed information:

```yaml
status:
  conditions:
  - lastTransitionTime: "2023-12-12T12:00:00Z"
    message: Failover to primary mode completed successfully
    reason: FailoverComplete
    status: "True"
    type: FailoverReady
  currentState: primary
  volumeReplicationStatuses:
  - currentState: primary
    error: ""
    lastUpdateTime: "2023-12-12T12:00:00Z" 
    name: volume-replication-1
  workloadStatuses:
  - kind: Deployment
    name: web-frontend
    state: Active
    error: "Deployment has 3 replicas"
    lastUpdateTime: "2023-12-12T12:00:00Z"
  - kind: StatefulSet
    name: database
    state: Active
    error: "StatefulSet has 2 replicas"
    lastUpdateTime: "2023-12-12T12:00:00Z"
  workloadStatus: "3/3 active"
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

```sh
make build-installer IMG=<some-registry>/failover-operator:tag
```

NOTE: The makefile target mentioned above generates an 'install.yaml'
file in the dist directory. This file contains all the resources built
with Kustomize, which are necessary to install this project without
its dependencies.

2. Using the installer

Users can just run kubectl apply -f <URL for YAML BUNDLE> to install the project, i.e.:

```sh
kubectl apply -f https://raw.githubusercontent.com/<org>/failover-operator/<tag or branch>/dist/install.yaml
```

## Contributing
// TODO(user): Add detailed information on how you would like others to contribute to this project

**NOTE:** Run `make help` for more information on all potential `make` targets

More information can be found via the [Kubebuilder Documentation](https://book.kubebuilder.io/introduction.html)

## License

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
