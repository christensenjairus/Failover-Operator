/*
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
*/

package failover

/*
FailoverStages documents the stages in the failover process.

This file serves as a central documentation point for the failover process,
detailing each stage, its responsibilities, and the interactions between
different cluster operators during a failover operation.

The operator supports two failover modes:
1. CONSISTENCY Mode (previously "Safe"): Prioritizes data consistency
2. UPTIME Mode (previously "Fast"): Prioritizes service availability

Each mode follows a different workflow to achieve its optimization goal.
*/

/*
CONSISTENCY Mode Workflow
=======================
This mode prioritizes data consistency and safety. The workflow ensures
that the source cluster is completely shut down before the target cluster
becomes active, guaranteeing that there's only one source of truth at any time.

Stage 1: Initialization Stage
-----------------------
Purpose: Prepare for failover by validating prerequisites and acquiring necessary locks.

Actions:
1. Validate the failover request and prerequisites
   - Verify source and target clusters exist
   - Check cluster health and availability
   - Ensure the target cluster can receive workloads

2. Acquire lock in DynamoDB for the FailoverGroup
   - Prevents concurrent failover operations on the same group
   - Lock is cluster-aware to allow different operators to coordinate

3. Read current configuration from DynamoDB
   - Load global state including current active cluster
   - Verify expected state matches actual state

4. Set FailoverGroup status to "IN_PROGRESS"
   - Update status in both Kubernetes CR and DynamoDB

5. Log the start of the failover operation
   - Record start time, initiator, and reason
   - Record that this is a CONSISTENCY mode failover

Validation Checks:
- Target cluster must exist and be in STANDBY role
- Target cluster must be healthy (OK or DEGRADED status)
- Source cluster should be in PRIMARY role
- No other failover should be in progress for this group

Outcomes:
- Failover is ready to proceed with exclusive access to the group
- Initial status is recorded for auditing and monitoring
*/

/*
Stage 2: Source Cluster Shutdown Stage (CONSISTENCY Mode)
-----------------------------------
Purpose: Completely deactivate the source cluster before making any changes to the target.

Actions:
1. Update network resources immediately
   - VirtualServices: Update annotations to disable DNS
     - Add "failover.io/dns-disabled: true" annotation
     - This prevents new traffic from reaching the services
   - Ingresses: Update annotations to disable DNS
     - Add "failover.io/dns-disabled: true" annotation
     - External-dns controllers will stop reconciling these resources

2. Scale down all workloads in source cluster
   - CronJobs: Suspend operations
     - Set .spec.suspend = true
   - Deployments: Scale to 0 replicas
     - Set .spec.replicas = 0
   - StatefulSets: Scale to 0 replicas
     - Set .spec.replicas = 0

3. Apply Flux annotations to prevent reconciliation
   - Add "fluxcd.io/ignore: "true"" annotation to applicable resources
   - Prevents Flux from overriding our changes during failover

4. Wait for workloads to be fully scaled down
   - Monitor replicas for each workload
   - Wait until all pods are terminated
   - Respect timeout settings from the FailoverGroup CR

Best Practices:
- Network changes should happen first to prevent traffic to in-shutdown workloads
- Monitor each workload type independently for scale-down completion
- Use exponential backoff when checking workload status

Outcomes:
- All workloads in source cluster are stopped
- Network is configured to prevent traffic to the source cluster
- The cluster is ready for volume operations
*/

/*
Stage 3: Volume Transition Stage (CONSISTENCY Mode)
-----------------------------------------
Purpose: Safely transition volumes from source to target cluster.

Actions:
1. Demote volumes in source cluster to Secondary
   - For each VolumeReplication resource:
     - Update .spec.replicationState = "secondary"
   - Track each volume's replication status

2. Wait for volumes to be demoted
   - Monitor .status.state of each VolumeReplication
   - Wait until all volumes report "secondary" state
   - Respect timeout settings from the FailoverGroup CR

3. Update DynamoDB to indicate volumes are ready for promotion
   - Set group.status.volumeState = "READY_FOR_PROMOTION"
   - Include timestamp and source cluster information

4. Promote volumes in target cluster to Primary
   - For each VolumeReplication resource:
     - Update .spec.replicationState = "primary"
   - Track each volume's promotion status

5. Wait for volumes to be promoted successfully
   - Monitor .status.state of each VolumeReplication
   - Wait until all volumes report "primary" state
   - Respect timeout settings from the FailoverGroup CR

6. Verify data availability
   - Perform basic read tests on volumes
   - Check filesystem mount status
   - Verify PVCs are bound and accessible

Technical Details:
- Volume demotion requires workloads to be fully stopped
- CSI drivers handle the actual replication role changes
- Different storage providers may have different behavior

Potential Issues:
- Volume demotion might fail if workloads are still using the volumes
- Some volumes might take longer to demote than others
- Network issues might prevent replication from completing

Outcomes:
- All volumes in source cluster are in Secondary role
- All volumes in target cluster are in Primary role
- Data is fully consistent and ready for use by target workloads
*/

/*
Stage 4: Target Cluster Activation Stage (CONSISTENCY Mode)
----------------------------------
Purpose: Activate workloads in the target cluster and restore network connectivity.

Actions:
1. Scale up workloads in target cluster
   - StatefulSets: Scale to desired replicas
     - Set .spec.replicas to original value from DynamoDB
   - Deployments: Scale to desired replicas
     - Set .spec.replicas to original value from DynamoDB
   - CronJobs: Resume operations
     - Set .spec.suspend = false

2. Trigger Flux reconciliation if specified
   - Remove "fluxcd.io/ignore: "true"" annotations
   - For resources with triggerReconcile=true:
     - Add annotation to force reconciliation

3. Wait for workloads to be ready
   - Monitor readiness probes for each pod
   - Check deployment/statefulset status conditions
   - Verify services are properly endpoints

4. Update network resources to enable DNS
   - VirtualServices: Remove "failover.io/dns-disabled" annotation
   - Ingresses: Remove "failover.io/dns-disabled" annotation
   - External-dns controllers will start reconciling these resources

Best Practices:
- Scale StatefulSets before Deployments (data tier first)
- Monitor each workload for successful startup
- Don't enable network until workloads are confirmed ready

Potential Issues:
- Pods might fail to start if volumes aren't properly promoted
- Readiness probes might timeout if dependent services aren't available
- Deployment strategies might affect startup time

Outcomes:
- All workloads in target cluster are running
- Network is configured to route traffic to the target cluster
- The application is fully operational in the target cluster
*/

/*
Stage 5: Completion Stage (CONSISTENCY Mode)
------------------
Purpose: Finalize the failover process and update records.

Actions:
1. Update DynamoDB Group Configuration with new owner
   - Set ActiveCluster to the target cluster name
   - Update cluster roles (PRIMARY/STANDBY)
   - Update timestamp of last successful failover

2. Write History record with metrics and details
   - Record total failover time
   - Record downtime (if any)
   - Store error logs if applicable
   - Record manual or automatic nature of failover

3. Release the lock
   - Remove failover lock from DynamoDB
   - Allow other operations to proceed

Outcomes:
- All records are updated to reflect the new cluster roles
- History is recorded for auditing and metrics
- System is ready for normal operation
*/

/*
UPTIME Mode Workflow
=================
This mode prioritizes service availability. The workflow activates the target
cluster before deactivating the source, minimizing service downtime but
potentially allowing a brief period where both clusters are active.

Stage 1: Initialization Stage
-----------------------
Purpose: Same as CONSISTENCY mode, but marked as UPTIME mode.

Actions:
1-5. Same as CONSISTENCY mode

Additional Validation Checks:
- Verify that data is sufficiently in sync to minimize potential data loss
- Verify that storage system supports multiple primary volumes

Outcomes:
- Failover is ready to proceed as an UPTIME mode operation
*/

/*
Stage 2: Target Cluster Preparation Stage (UPTIME Mode)
-----------------------------------
Purpose: Prepare the target cluster to take over service before deactivating source.

Actions:
1. Promote volumes in target cluster to Primary
   - Both clusters will temporarily have PRIMARY volumes
   - For each VolumeReplication resource:
     - Update .spec.replicationState = "primary"
   - Track each volume's promotion status

2. Wait for volumes to be promoted successfully
   - Monitor .status.state of each VolumeReplication
   - Wait until all volumes report "primary" state

3. Verify data availability
   - Perform basic read tests on volumes
   - Check filesystem mount status
   - Verify PVCs are bound and accessible

4. Scale up workloads in target cluster
   - StatefulSets: Scale to desired replicas
   - Deployments: Scale to desired replicas
   - CronJobs: Resume operations

5. Wait for target workloads to be ready
   - Monitor readiness probes for each pod
   - Check deployment/statefulset status conditions
   - Verify services have proper endpoints

6. Mark target as ready for traffic in DynamoDB
   - Set state to indicate target can receive traffic

Technical Details:
- In this stage, both clusters have PRIMARY volumes temporarily
- This creates a brief window of potential write conflicts
- Storage system must support this configuration

Potential Issues:
- Data inconsistency if both clusters receive writes
- Some storage providers may not support multiple primaries

Outcomes:
- Target cluster is fully prepared to handle traffic
- Both clusters are now capable of serving requests
*/

/*
Stage 3: Traffic Transition Stage (UPTIME Mode)
----------------------------------
Purpose: Redirect traffic to target cluster with minimal disruption.

Actions:
1. Wait for target to be ready for traffic
   - Check DynamoDB state to confirm target preparation is complete

2. Update network resources to enable DNS for target cluster
   - VirtualServices: Update DNS for target cluster
   - Ingresses: Update DNS for target cluster
   - External-dns controllers will reconcile these resources

3. Mark traffic transition as complete in DynamoDB
   - Signal that traffic is now flowing to target cluster

Technical Details:
- Network changes are made only after target is fully ready
- May involve DNS changes or load balancer reconfiguration
- Timing is critical to minimize request failures

Potential Issues:
- DNS propagation delays may cause brief routing issues
- Some clients may cache previous DNS entries

Outcomes:
- Traffic is now flowing to the target cluster
- Users experience minimal or no service disruption
*/

/*
Stage 4: Source Cluster Deactivation Stage (UPTIME Mode)
-----------------------------------------
Purpose: Safely deactivate source cluster after target is fully serving traffic.

Actions:
1. Wait for traffic transition to be complete
   - Check DynamoDB to confirm traffic is flowing to target cluster

2. Update network resources to disable DNS for source cluster
   - VirtualServices: Update annotations to disable DNS
   - Ingresses: Update annotations to disable DNS
   - This prevents new traffic from reaching the source cluster

3. Scale down workloads in source cluster
   - CronJobs: Suspend operations
   - Deployments: Scale to 0 replicas
   - StatefulSets: Scale to 0 replicas

4. Wait for workloads to be fully scaled down
   - Monitor replicas for each workload
   - Wait until all pods are terminated

5. Demote volumes in source cluster to Secondary
   - For each VolumeReplication resource:
     - Update .spec.replicationState = "secondary"

6. Wait for volumes to be demoted
   - Monitor .status.state of each VolumeReplication
   - Wait until all volumes report "secondary" state

Technical Details:
- Source cluster is deactivated only after target is confirmed operational
- This reverses the typical order of the CONSISTENCY mode workflow

Potential Issues:
- If target cluster fails after traffic switch but before source deactivation,
  rapid rollback to source cluster is possible
- Volume demotion might take time after workloads are scaled down

Outcomes:
- Source cluster is fully deactivated
- All volumes are back in their proper roles (single PRIMARY)
- System has completed transition with minimal downtime
*/

/*
Stage 5: Completion Stage (UPTIME Mode)
------------------
Purpose: Same as CONSISTENCY mode.

Actions:
1-3. Same as CONSISTENCY mode

Additional Record:
- Document the temporary period of dual PRIMARY volumes
- Record minimal downtime metric

Outcomes:
- All records are updated to reflect the new cluster roles
- History is recorded with emphasis on service continuity
- System is ready for normal operation
*/

/*
Error Handling (Any Stage)
--------------------------
When an error occurs during any stage of failover:

1. The error is logged with detailed context and stack trace
2. The failover status is updated to FAILED with the error message
3. Cleanup actions are taken based on how far the failover progressed:
   - Stage 1 (Initialization): Release lock, update status
   - Stage 2 (Source/Target Preparation): Restore to initial state if possible
   - Stage 3 (Volume/Traffic Transition): Requires manual intervention
   - Stage 4 (Target/Source Activation/Deactivation): Requires manual intervention
   - Stage 5 (Completion): Release resources, ensure locks are freed

4. The error is recorded in the failover history
5. Appropriate alerts are triggered based on error severity
6. The controller will not retry automatically to avoid cascade failures

Special considerations for UPTIME mode errors:
- If error occurs after traffic transition but before source deactivation,
  the system is in a stable but incomplete state
- Manual intervention will be required to complete the failover or rollback
*/
