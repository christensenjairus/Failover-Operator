# Configuration System for Failover Operator

This document explains how to configure the Failover Operator using the ConfigMap configuration system.

## Configuration Sources

The Failover Operator has a layered configuration system with the following priority (highest to lowest):

1. AWS Credentials from Kubernetes Secret (for AWS-specific configuration)
2. ConfigMap settings 
3. Environment variables (defined in the deployment manifest)
4. Default values (built into the operator)

**RECOMMENDED APPROACH**: Use ConfigMaps for all general configuration and Secrets for sensitive credentials. This approach is preferred over modifying the deployment YAML directly.

## Configuring with ConfigMaps

### Step 1: Create a ConfigMap

Create a ConfigMap with your desired configuration. You can customize this example:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: failover-operator-config
  namespace: system
data:
  # AWS Configuration
  AWS_REGION: "us-west-2"
  # AWS_ENDPOINT: "http://localhost:8000"  # Uncomment for local DynamoDB
  # AWS_USE_LOCAL_ENDPOINT: "true"         # Uncomment for local DynamoDB
  
  # DynamoDB Configuration
  DYNAMODB_TABLE_NAME: "failover-operator"
  
  # Operator Configuration
  CLUSTER_NAME: "primary-cluster"          # Override the cluster name
  OPERATOR_ID: "failover-operator"
  
  # Timeouts and intervals
  RECONCILE_INTERVAL: "30s"
  DEFAULT_HEARTBEAT_INTERVAL: "30s"
```

Save this to a file (e.g., `operator-config.yaml`) and apply it:

```bash
kubectl apply -f operator-config.yaml
```

### Step 2: Create an AWS Credentials Secret (if using AWS)

Create a Secret containing your AWS credentials:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: aws-credentials
  namespace: system
type: Opaque
data:
  # These values must be base64 encoded
  # Example: echo -n "your-access-key" | base64
  access_key: <base64-encoded-access-key>
  secret_key: <base64-encoded-secret-key>
  # Optional fields
  region: <base64-encoded-region>  # e.g., base64 encoded "us-west-2"
```

Save this to a file (e.g., `aws-credentials.yaml`) and apply it:

```bash
kubectl apply -f aws-credentials.yaml
```

### Step 3: Update the Operator Deployment

When deploying the operator, configure it to use your ConfigMap and Secret (if applicable):

```bash
# Install or upgrade using Helm (if you're using Helm)
helm upgrade --install failover-operator ./charts/failover-operator \
  --set config.configMapName=failover-operator-config \
  --set config.configMapNamespace=system \
  --set config.awsSecretName=aws-credentials \
  --set config.awsSecretNamespace=system

# Or directly with kubectl by patching the deployment
kubectl patch deployment failover-operator -n system --type json \
  -p '[{"op": "add", "path": "/spec/template/spec/containers/0/args/-", "value": "--config-map-name=failover-operator-config"}, {"op": "add", "path": "/spec/template/spec/containers/0/args/-", "value": "--config-map-namespace=system"}, {"op": "add", "path": "/spec/template/spec/containers/0/args/-", "value": "--aws-secret-name=aws-credentials"}, {"op": "add", "path": "/spec/template/spec/containers/0/args/-", "value": "--aws-secret-namespace=system"}]'
```

## Available Configuration Options

| Config Key | Description | Default Value | Example |
|------------|-------------|---------------|---------|
| **AWS Configuration** |
| AWS_REGION | AWS region for DynamoDB | us-west-2 | us-east-1 |
| AWS_ENDPOINT | Optional custom endpoint for DynamoDB | "" | http://localhost:8000 |
| AWS_USE_LOCAL_ENDPOINT | Use local DynamoDB endpoint | false | true |
| **DynamoDB Configuration** |
| DYNAMODB_TABLE_NAME | DynamoDB table name | failover-operator | my-failover-table |
| **Operator Configuration** |
| CLUSTER_NAME | Kubernetes cluster name | default-cluster | production-cluster |
| OPERATOR_ID | Unique identifier for the operator | failover-operator | prod-failover-operator |
| **Timeouts and Intervals** |
| RECONCILE_INTERVAL | Controller reconciliation interval | 30s | 1m |
| DEFAULT_HEARTBEAT_INTERVAL | Default heartbeat interval | 30s | 15s |

## Updating Configuration

You can update the ConfigMap at any time with new settings:

```bash
kubectl edit configmap failover-operator-config -n system
```

After updating, restart the operator to apply the changes:

```bash
kubectl rollout restart deployment failover-operator -n system
```

## Configuration Best Practices

1. **Use ConfigMaps for All Settings**: Put all your configuration in ConfigMaps rather than editing deployment YAML.

2. **Use Secrets for Credentials**: Always store AWS credentials or other sensitive information in Kubernetes Secrets.

3. **Use a Version Control System**: Store your ConfigMap and Secret definitions (with placeholders for actual secrets) in a version control system.

4. **Environment-Specific ConfigMaps**: Create different ConfigMaps for different environments (dev, staging, production).

5. **Documentation**: Document changes to configuration and reasons for changes.

6. **Validation**: The operator validates configuration values when loading. Invalid settings will fall back to defaults.

## Multi-Cluster Setup

When configuring the Failover Operator across multiple clusters, use different values for `CLUSTER_NAME` in each cluster's ConfigMap:

```yaml
# Primary cluster
CLUSTER_NAME: "primary-cluster"

# Secondary cluster
CLUSTER_NAME: "secondary-cluster"
```

## Troubleshooting

If the operator fails to load configuration from the ConfigMap, it will:

1. Log a warning with the error message
2. Fall back to environment variables and default values
3. Continue operating with the fallback configuration

To check if your configuration is loaded correctly, check the operator logs:

```bash
kubectl logs deployment/failover-operator -n system
```

Look for log lines containing "Operator configuration loaded" to see the active configuration.

## FailoverGroup Custom Resource

The FailoverGroup custom resource defines a group of resources that should be failed over together. Here's the updated structure of the FailoverGroup CR:

```yaml
apiVersion: crd.hahomelabs.com/v1alpha1
kind: FailoverGroup
metadata:
  name: example
  namespace: default
spec:
  # Single failover mode for the entire group: "safe" or "fast"
  # "safe" ensures data is fully synced before failover
  # "fast" allows immediate transition without waiting
  failoverMode: "safe"
  
  # Optional operator identifier
  operatorID: "failover-operator"
  
  # Optional timeout settings for automatic operations
  timeouts:
    transitoryState: "5m"     # Maximum time a FailoverGroup can remain in FAILOVER/FAILBACK states
    unhealthyPrimary: "2m"    # Time a PRIMARY cluster can remain unhealthy before auto-failover
    heartbeat: "1m"           # Time without heartbeats before assuming a cluster is down
  
  # Optional heartbeat interval
  heartbeatInterval: "30s"
  
  # Optional flag to disable automatic failovers (manual override for maintenance)
  suspended: false
  
  # Optional documentation field explaining why automatic failovers are suspended
  suspensionReason: ""
  
  # Workloads that need scaling and health tracking during failover
  workloads:
    - kind: StatefulSet       # Supported kinds: Deployment, StatefulSet, CronJob
      name: example-db
      volumeReplications:     # Optional list of volume replications
        - example-db-data
    
    - kind: Deployment
      name: example-frontend
      
    - kind: CronJob
      name: example-backup
  
  # Network resources that just need annotation flips during failover
  networkResources:
    - kind: VirtualService    # Supported kinds: VirtualService, Ingress
      name: example-vs
    
    - kind: Ingress
      name: example-ingress
      
  # Flux resources to manage during failover
  fluxResources:
    - kind: HelmRelease       # Supported kinds: HelmRelease, Kustomization
      name: example-helm
      triggerReconcile: true  # Whether to trigger reconciliation during failover
      
    - kind: Kustomization
      name: example-config
      triggerReconcile: false
```

### Resource Management During Failover

The Failover Operator manages different resources during failover as follows:

1. **Workloads**:
   - `Deployment`: Scaled up/down during failover
   - `StatefulSet`: Scaled up/down during failover
   - `CronJob`: Suspended/resumed during failover

2. **Storage**:
   - `VolumeReplication`: Promoted/demoted during failover (as part of workload config)

3. **Network**:
   - `VirtualService`: Simple annotation change
   - `Ingress`: Simple annotation change

4. **GitOps**:
   - `HelmRelease`: Suspended/resumed and optional reconciliation trigger
   - `Kustomization`: Suspended/resumed and optional reconciliation trigger

### Status Reporting

The FailoverGroup reports status information for each resource type:

```yaml
status:
  state: "PRIMARY"            # "PRIMARY", "STANDBY", "FAILOVER", "FAILBACK"
  health: "OK"                # "OK", "DEGRADED", "ERROR"
  
  # Status information for each workload
  workloads:
    - kind: "StatefulSet"
      name: "example-db"
      health: "OK"
      status: "Running normally"
      volumeReplications:
        - name: "example-db-data"
          health: "OK"
          status: "Replication in sync"
  
  # Status information for each network resource
  networkResources:
    - kind: "VirtualService"
      name: "example-vs"
      health: "OK"
      status: "Traffic flowing correctly"
  
  # Status information for each Flux resource
  fluxResources:
    - kind: "HelmRelease"
      name: "example-helm"
      health: "OK"
      status: "Reconciled successfully"
  
  # Information about when the last failover occurred
  lastFailoverTime: "2023-04-01T12:00:00Z"
  
  # Global state information synced from DynamoDB
  globalState:
    activeCluster: "primary-cluster"
    thisCluster: "primary-cluster"
    dbSyncStatus: "Synced"
    lastSyncTime: "2023-04-01T12:00:00Z"
    clusters:
      - name: "primary-cluster"
        role: "PRIMARY"
        health: "OK"
        lastHeartbeat: "2023-04-01T12:00:00Z"
      - name: "standby-cluster"
        role: "STANDBY"
        health: "OK"
        lastHeartbeat: "2023-04-01T12:00:00Z"
``` 