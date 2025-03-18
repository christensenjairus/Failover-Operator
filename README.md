# Failover Operator

A Kubernetes operator for managing site failover with volume replication and workload management.

## Overview

The Failover Operator manages site failovers by:

1. Coordinating the failover of volume replications
2. Managing workloads (Deployments, StatefulSets, CronJobs)
3. Managing Flux resources (HelmReleases, Kustomizations)
4. Updating Istio VirtualServices

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
- VolumeReplications are set to `secondary-ro` mode
- Deployments and StatefulSets are scaled down to 0 replicas
- CronJobs are suspended
- Flux resources are suspended
- VirtualServices are updated to stop routing traffic to this site

### Resource References

Each resource in `managedResources` is defined with:
- `kind`: The type of resource (required)
- `name`: The name of the resource (required)
- `namespace`: The namespace of the resource (optional, defaults to the FailoverPolicy's namespace)
- `apiGroup`: The API group of the resource (optional, inferred from kind if not specified)

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
