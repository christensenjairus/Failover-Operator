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

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	crdv1alpha1 "github.com/christensenjairus/Failover-Operator/api/v1alpha1"
	"github.com/christensenjairus/Failover-Operator/internal/controller/cronjobs"
	"github.com/christensenjairus/Failover-Operator/internal/controller/deployments"
	"github.com/christensenjairus/Failover-Operator/internal/controller/dynamodb"
	"github.com/christensenjairus/Failover-Operator/internal/controller/helmreleases"
	"github.com/christensenjairus/Failover-Operator/internal/controller/ingresses"
	"github.com/christensenjairus/Failover-Operator/internal/controller/kustomizations"
	"github.com/christensenjairus/Failover-Operator/internal/controller/statefulsets"
	"github.com/christensenjairus/Failover-Operator/internal/controller/virtualservices"
	"github.com/christensenjairus/Failover-Operator/internal/controller/volumereplications"
)

// Manager handles failover operations
type Manager struct {
	client.Client
	Log         logr.Logger
	ClusterName string // Current cluster name

	// Resource Managers
	DeploymentsManager        *deployments.Manager
	StatefulSetsManager       *statefulsets.Manager
	CronJobsManager           *cronjobs.Manager
	KustomizationsManager     *kustomizations.Manager
	HelmReleasesManager       *helmreleases.Manager
	VirtualServicesManager    *virtualservices.Manager
	VolumeReplicationsManager *volumereplications.Manager
	DynamoDBManager           *dynamodb.DynamoDBService
	IngressesManager          *ingresses.Manager
}

// NewManager creates a new failover Manager
func NewManager(client client.Client, clusterName string, log logr.Logger) *Manager {
	return &Manager{
		Client:                    client,
		Log:                       log,
		ClusterName:               clusterName,
		DeploymentsManager:        deployments.NewManager(client),
		StatefulSetsManager:       statefulsets.NewManager(client),
		CronJobsManager:           cronjobs.NewManager(client),
		KustomizationsManager:     kustomizations.NewManager(client),
		HelmReleasesManager:       helmreleases.NewManager(client),
		VirtualServicesManager:    virtualservices.NewManager(client),
		VolumeReplicationsManager: volumereplications.NewManager(client),
		IngressesManager:          ingresses.NewManager(client),
		// DynamoDB manager typically initialized separately with AWS credentials
	}
}

// SetDynamoDBManager sets the DynamoDB manager instance
func (m *Manager) SetDynamoDBManager(dbManager *dynamodb.DynamoDBService) {
	m.DynamoDBManager = dbManager
}

// ProcessFailover handles the main failover workflow for all failover groups
func (m *Manager) ProcessFailover(ctx context.Context, failover *crdv1alpha1.Failover) error {
	log := m.Log.WithValues("failover", failover.Name, "namespace", failover.Namespace)
	log.Info("Processing failover request", "targetCluster", failover.Spec.TargetCluster)

	// Update status to in progress if not already set
	if failover.Status.Status != "IN_PROGRESS" {
		if err := m.updateFailoverStatus(ctx, failover, "IN_PROGRESS", nil); err != nil {
			return err
		}
	}

	startTime := time.Now()

	// Process each failover group
	var processingErrors []error
	for i, group := range failover.Spec.FailoverGroups {
		log := log.WithValues("failoverGroup", group.Name, "namespace", group.Namespace)
		log.Info("Processing failover group")

		// Update group status to in progress
		failover.Status.FailoverGroups[i].Status = "IN_PROGRESS"
		failover.Status.FailoverGroups[i].StartTime = time.Now().Format(time.RFC3339)
		if err := m.Client.Status().Update(ctx, failover); err != nil {
			log.Error(err, "Failed to update failover group status")
			processingErrors = append(processingErrors, err)
			continue
		}

		// Get the failover group
		failoverGroup := &crdv1alpha1.FailoverGroup{}
		groupNamespace := group.Namespace
		if groupNamespace == "" {
			groupNamespace = failover.Namespace
		}

		if err := m.Client.Get(ctx, client.ObjectKey{
			Namespace: groupNamespace,
			Name:      group.Name,
		}, failoverGroup); err != nil {
			log.Error(err, "Failed to get failover group")

			failover.Status.FailoverGroups[i].Status = "FAILED"
			failover.Status.FailoverGroups[i].Message = fmt.Sprintf("Failed to get failover group: %v", err)
			processingErrors = append(processingErrors, err)
			continue
		}

		// Verify this failover should be processed by this operator instance
		// Skip if the operator ID doesn't match
		if failoverGroup.Spec.OperatorID != "" && m.DynamoDBManager != nil &&
			failoverGroup.Spec.OperatorID != m.DynamoDBManager.OperatorID {
			log.Info("Skipping failover group - operator ID mismatch",
				"groupOperatorID", failoverGroup.Spec.OperatorID,
				"thisOperatorID", m.DynamoDBManager.OperatorID)
			continue
		}

		// Process the failover for this group
		groupErr := m.processFailoverGroup(ctx, failover, failoverGroup, i)
		if groupErr != nil {
			processingErrors = append(processingErrors, groupErr)
		}
	}

	// Calculate metrics
	endTime := time.Now()
	failover.Status.Metrics.TotalFailoverTimeSeconds = int64(endTime.Sub(startTime).Seconds())

	// Update final status
	finalStatus := "SUCCESS"
	if len(processingErrors) > 0 {
		finalStatus = "FAILED"
	}

	return m.updateFailoverStatus(ctx, failover, finalStatus, processingErrors)
}

// processFailoverGroup handles the failover logic for a single FailoverGroup
func (m *Manager) processFailoverGroup(ctx context.Context,
	failover *crdv1alpha1.Failover,
	failoverGroup *crdv1alpha1.FailoverGroup,
	groupIndex int) error {

	log := m.Log.WithValues("failoverGroup", failoverGroup.Name, "namespace", failoverGroup.Namespace)

	// 1. Verify prerequisites and acquire lock
	if err := m.verifyAndAcquireLock(ctx, failoverGroup, failover.Spec.TargetCluster); err != nil {
		failover.Status.FailoverGroups[groupIndex].Status = "FAILED"
		failover.Status.FailoverGroups[groupIndex].Message = fmt.Sprintf("Failed to verify prerequisites or acquire lock: %v", err)
		if err := m.Client.Status().Update(ctx, failover); err != nil {
			log.Error(err, "Failed to update failover status")
		}
		return err
	}

	// 2. Execute the failover workflow
	startTime := time.Now()
	err := m.executeFailoverWorkflow(ctx, failoverGroup, failover)
	endTime := time.Now()

	// 3. Update metrics and status
	downtime := int64(endTime.Sub(startTime).Seconds())
	if failover.Status.Metrics.TotalDowntimeSeconds < downtime {
		failover.Status.Metrics.TotalDowntimeSeconds = downtime
	}

	if err != nil {
		failover.Status.FailoverGroups[groupIndex].Status = "FAILED"
		failover.Status.FailoverGroups[groupIndex].Message = fmt.Sprintf("Failover execution failed: %v", err)
	} else {
		failover.Status.FailoverGroups[groupIndex].Status = "SUCCESS"
		failover.Status.FailoverGroups[groupIndex].CompletionTime = endTime.Format(time.RFC3339)
	}

	// 4. Release the lock
	if releaseLockErr := m.releaseLock(ctx, failoverGroup); releaseLockErr != nil {
		log.Error(releaseLockErr, "Failed to release failover group lock")
		// Don't return this error as it shouldn't override the main failover result
	}

	if err := m.Client.Status().Update(ctx, failover); err != nil {
		log.Error(err, "Failed to update failover status")
		return err
	}

	return err
}

// verifyAndAcquireLock verifies prerequisites and acquires a lock for the failover operation
func (m *Manager) verifyAndAcquireLock(ctx context.Context,
	failoverGroup *crdv1alpha1.FailoverGroup,
	targetCluster string) error {

	// Create a function to verify prerequisites
	// - Check if source and target clusters exist in the group's global state
	// - Verify that the target cluster is available
	// - Check that the current active cluster matches our expectation

	// Acquire a lock in DynamoDB to prevent concurrent operations
	// This ensures only one failover operation can run at a time for this group

	return nil
}

// executeFailoverWorkflow executes the failover workflow for a group
func (m *Manager) executeFailoverWorkflow(ctx context.Context,
	failoverGroup *crdv1alpha1.FailoverGroup,
	failover *crdv1alpha1.Failover) error {

	// Determine failover mode (safe or fast)
	// This can be used to modify behavior in specific stages (e.g., for data sync waiting)
	// Currently, our implementation follows the same path regardless of mode
	// but we keep track of it for potential future optimizations
	_ = failoverGroup.Spec.FailoverMode
	if failover.Spec.ForceFastMode {
		// This would change the mode to "fast" if needed in the future
	}

	// STAGE 1: INITIALIZATION
	// ==============================================
	// Already handled in previous steps:
	// - validate failover request and prerequisites (verifyAndAcquireLock)
	// - acquire lock in DynamoDB (verifyAndAcquireLock)
	// - read current configuration from DynamoDB
	// - set FailoverGroup status to "IN_PROGRESS"
	// - log the start of the failover operation

	// STAGE 2: SOURCE CLUSTER PREPARATION
	// ==============================================
	// Update network resources immediately to disable DNS
	if err := m.updateNetworkResourcesForSourceCluster(ctx, failoverGroup, failover); err != nil {
		return fmt.Errorf("stage 2 - failed to update network resources for source cluster: %w", err)
	}

	// Scale down all workloads in source cluster
	if err := m.scaleDownWorkloads(ctx, failoverGroup, failover); err != nil {
		return fmt.Errorf("stage 2 - failed to scale down workloads: %w", err)
	}

	// Apply Flux annotations to prevent reconciliation
	if err := m.disableFluxReconciliation(ctx, failoverGroup, failover); err != nil {
		return fmt.Errorf("stage 2 - failed to disable Flux reconciliation: %w", err)
	}

	// Wait for workloads to be fully scaled down
	if err := m.waitForWorkloadsScaledDown(ctx, failoverGroup, failover); err != nil {
		return fmt.Errorf("stage 2 - failed waiting for workloads to scale down: %w", err)
	}

	// STAGE 3: VOLUME DEMOTION (SOURCE CLUSTER)
	// ==============================================
	// Demote volumes in source cluster to Secondary
	if err := m.demoteVolumes(ctx, failoverGroup, failover); err != nil {
		return fmt.Errorf("stage 3 - failed to demote volumes: %w", err)
	}

	// Wait for volumes to be demoted
	if err := m.waitForVolumesDemoted(ctx, failoverGroup, failover); err != nil {
		return fmt.Errorf("stage 3 - failed waiting for volumes to be demoted: %w", err)
	}

	// Update DynamoDB to indicate volumes are ready for promotion
	if err := m.markVolumesReadyForPromotion(ctx, failoverGroup, failover); err != nil {
		return fmt.Errorf("stage 3 - failed to mark volumes as ready for promotion: %w", err)
	}

	// Release control to target cluster's operator
	// This step concludes source cluster's responsibility
	// Target cluster's operator will pick up from here

	// STAGE 4: VOLUME PROMOTION (TARGET CLUSTER)
	// ==============================================
	// If this is the target cluster's operator, perform promotion
	if m.ClusterName == failover.Spec.TargetCluster {
		// Wait until source cluster indicates volumes are ready for promotion
		if err := m.waitForVolumesReadyForPromotion(ctx, failoverGroup, failover); err != nil {
			return fmt.Errorf("stage 4 - failed waiting for volumes to be ready for promotion: %w", err)
		}

		// Promote volumes in target cluster to Primary
		if err := m.promoteVolumes(ctx, failoverGroup, failover); err != nil {
			return fmt.Errorf("stage 4 - failed to promote volumes: %w", err)
		}

		// Wait for volumes to be promoted successfully
		if err := m.waitForVolumesPromoted(ctx, failoverGroup, failover); err != nil {
			return fmt.Errorf("stage 4 - failed waiting for volumes to be promoted: %w", err)
		}

		// Verify data availability
		if err := m.verifyDataAvailability(ctx, failoverGroup, failover); err != nil {
			return fmt.Errorf("stage 4 - failed to verify data availability: %w", err)
		}

		// STAGE 5: TARGET CLUSTER ACTIVATION
		// ==============================================
		// Scale up workloads in target cluster
		if err := m.scaleUpWorkloads(ctx, failoverGroup, failover); err != nil {
			return fmt.Errorf("stage 5 - failed to scale up workloads: %w", err)
		}

		// Trigger Flux reconciliation if specified
		if err := m.triggerFluxReconciliation(ctx, failoverGroup, failover); err != nil {
			return fmt.Errorf("stage 5 - failed to trigger Flux reconciliation: %w", err)
		}

		// Wait for workloads to be ready
		if err := m.waitForTargetReady(ctx, failoverGroup, failover); err != nil {
			return fmt.Errorf("stage 5 - failed waiting for target to be ready: %w", err)
		}

		// Update network resources to enable DNS
		if err := m.updateNetworkResourcesForTargetCluster(ctx, failoverGroup, failover); err != nil {
			return fmt.Errorf("stage 5 - failed to update network resources for target cluster: %w", err)
		}
	}

	// STAGE 6: COMPLETION
	// ==============================================
	// Update DynamoDB Group Configuration with new owner
	if err := m.updateGlobalState(ctx, failoverGroup, failover); err != nil {
		return fmt.Errorf("stage 6 - failed to update global state: %w", err)
	}

	// Write History record with metrics and details
	if err := m.recordFailoverHistory(ctx, failoverGroup, failover); err != nil {
		return fmt.Errorf("stage 6 - failed to record failover history: %w", err)
	}

	// Release the lock (done in calling function)
	// Set FailoverGroup status to "SUCCESS" (done in calling function)
	// Log completion of failover operation (done in calling function)

	return nil
}

// updateNetworkResourcesForSourceCluster updates network resources for the source cluster
// to disable DNS routing during failover
func (m *Manager) updateNetworkResourcesForSourceCluster(ctx context.Context,
	failoverGroup *crdv1alpha1.FailoverGroup,
	failover *crdv1alpha1.Failover) error {
	// Update VirtualServices and Ingresses to disable DNS
	return nil
}

// disableFluxReconciliation applies annotations to prevent Flux from reconciling
// resources during the failover
func (m *Manager) disableFluxReconciliation(ctx context.Context,
	failoverGroup *crdv1alpha1.FailoverGroup,
	failover *crdv1alpha1.Failover) error {
	// Apply appropriate annotations to Flux resources
	return nil
}

// waitForWorkloadsScaledDown waits for all workloads to be fully scaled down
func (m *Manager) waitForWorkloadsScaledDown(ctx context.Context,
	failoverGroup *crdv1alpha1.FailoverGroup,
	failover *crdv1alpha1.Failover) error {
	// Check that all workloads have scaled to 0
	return nil
}

// demoteVolumes demotes volumes in the source cluster to Secondary role
func (m *Manager) demoteVolumes(ctx context.Context,
	failoverGroup *crdv1alpha1.FailoverGroup,
	failover *crdv1alpha1.Failover) error {
	// Perform volume demotion operations
	return nil
}

// waitForVolumesDemoted waits for all volumes to be successfully demoted
func (m *Manager) waitForVolumesDemoted(ctx context.Context,
	failoverGroup *crdv1alpha1.FailoverGroup,
	failover *crdv1alpha1.Failover) error {
	// Monitor volume status until all are demoted
	return nil
}

// markVolumesReadyForPromotion updates DynamoDB to indicate volumes are ready
// for promotion in the target cluster
func (m *Manager) markVolumesReadyForPromotion(ctx context.Context,
	failoverGroup *crdv1alpha1.FailoverGroup,
	failover *crdv1alpha1.Failover) error {
	// Update status in DynamoDB
	return nil
}

// waitForVolumesReadyForPromotion waits until volumes are ready to be promoted
// in the target cluster (target cluster operator function)
func (m *Manager) waitForVolumesReadyForPromotion(ctx context.Context,
	failoverGroup *crdv1alpha1.FailoverGroup,
	failover *crdv1alpha1.Failover) error {
	// Check DynamoDB status until volumes are ready
	return nil
}

// promoteVolumes promotes volumes in the target cluster to Primary role
func (m *Manager) promoteVolumes(ctx context.Context,
	failoverGroup *crdv1alpha1.FailoverGroup,
	failover *crdv1alpha1.Failover) error {
	// Perform volume promotion operations
	return nil
}

// waitForVolumesPromoted waits for all volumes to be successfully promoted
func (m *Manager) waitForVolumesPromoted(ctx context.Context,
	failoverGroup *crdv1alpha1.FailoverGroup,
	failover *crdv1alpha1.Failover) error {
	// Monitor volume status until all are promoted
	return nil
}

// verifyDataAvailability verifies that data is accessible after volume promotion
func (m *Manager) verifyDataAvailability(ctx context.Context,
	failoverGroup *crdv1alpha1.FailoverGroup,
	failover *crdv1alpha1.Failover) error {
	// Verify data is accessible
	return nil
}

// triggerFluxReconciliation triggers reconciliation of Flux resources
func (m *Manager) triggerFluxReconciliation(ctx context.Context,
	failoverGroup *crdv1alpha1.FailoverGroup,
	failover *crdv1alpha1.Failover) error {
	// Trigger Flux reconciliation for applicable resources
	return nil
}

// updateNetworkResourcesForTargetCluster updates network resources for the target cluster
// to enable DNS routing after failover
func (m *Manager) updateNetworkResourcesForTargetCluster(ctx context.Context,
	failoverGroup *crdv1alpha1.FailoverGroup,
	failover *crdv1alpha1.Failover) error {
	// Update VirtualServices and Ingresses to enable DNS
	return nil
}

// recordFailoverHistory records the failover operation details and metrics
func (m *Manager) recordFailoverHistory(ctx context.Context,
	failoverGroup *crdv1alpha1.FailoverGroup,
	failover *crdv1alpha1.Failover) error {
	// Record failover history in DynamoDB
	return nil
}

// scaleDownWorkloads scales down workloads in the source cluster
func (m *Manager) scaleDownWorkloads(ctx context.Context,
	failoverGroup *crdv1alpha1.FailoverGroup,
	failover *crdv1alpha1.Failover) error {

	// For each workload in the failover group:
	// 1. Identify whether it needs to be scaled down (in source cluster)
	// 2. If yes, scale down based on workload type:
	//    - For Deployments: Scale replicas to 0
	//    - For StatefulSets: Scale replicas to 0
	//    - For CronJobs: Suspend the job

	return nil
}

// waitForDataSync waits for data synchronization to complete
func (m *Manager) waitForDataSync(ctx context.Context,
	failoverGroup *crdv1alpha1.FailoverGroup,
	failover *crdv1alpha1.Failover) error {

	// For each volume replication:
	// 1. Monitor replication status
	// 2. Wait until data is synchronized or timeout is reached
	// 3. If force is enabled, skip waiting for full sync

	return nil
}

// scaleUpWorkloads scales up workloads in the target cluster
func (m *Manager) scaleUpWorkloads(ctx context.Context,
	failoverGroup *crdv1alpha1.FailoverGroup,
	failover *crdv1alpha1.Failover) error {

	// For each workload in the failover group:
	// 1. Identify whether it needs to be scaled up (in target cluster)
	// 2. If yes, scale up based on workload type:
	//    - For Deployments: Scale to desired replicas
	//    - For StatefulSets: Scale to desired replicas
	//    - For CronJobs: Resume the job

	return nil
}

// waitForTargetReady waits for the target cluster workloads to be ready
func (m *Manager) waitForTargetReady(ctx context.Context,
	failoverGroup *crdv1alpha1.FailoverGroup,
	failover *crdv1alpha1.Failover) error {

	// For each workload in the target cluster:
	// 1. Monitor readiness status
	// 2. Wait until workloads are ready or timeout is reached

	return nil
}

// handleFluxResources manages Flux GitOps resources during failover
func (m *Manager) handleFluxResources(ctx context.Context,
	failoverGroup *crdv1alpha1.FailoverGroup,
	failover *crdv1alpha1.Failover) error {

	// For each Flux resource in the failover group:
	// 1. Check if reconciliation is needed
	// 2. If yes, trigger reconciliation based on resource type:
	//    - For HelmReleases: Trigger reconciliation
	//    - For Kustomizations: Trigger reconciliation

	return nil
}

// updateNetworkResources updates network resources to point to the new primary
func (m *Manager) updateNetworkResources(ctx context.Context,
	failoverGroup *crdv1alpha1.FailoverGroup,
	failover *crdv1alpha1.Failover) error {

	// For each network resource in the failover group:
	// 1. Update routing based on resource type:
	//    - For VirtualServices: Update route destination
	//    - For Ingresses: Update backend services

	return nil
}

// updateGlobalState updates the global state in DynamoDB
func (m *Manager) updateGlobalState(ctx context.Context,
	failoverGroup *crdv1alpha1.FailoverGroup,
	failover *crdv1alpha1.Failover) error {

	// 1. Update the active cluster to the new primary
	// 2. Record failover details (timestamp, reason, etc.)
	// 3. Update cluster roles (PRIMARY/STANDBY)

	return nil
}

// updateFailoverGroupStatus updates the status of the failover group
func (m *Manager) updateFailoverGroupStatus(ctx context.Context,
	failoverGroup *crdv1alpha1.FailoverGroup,
	failover *crdv1alpha1.Failover) error {

	// 1. Update failover group status to reflect the new state
	// 2. Update workload statuses
	// 3. Update last failover time

	return nil
}

// releaseLock releases the lock for the failover group
func (m *Manager) releaseLock(ctx context.Context,
	failoverGroup *crdv1alpha1.FailoverGroup) error {

	// Release the lock in DynamoDB
	// This allows other operations to proceed

	return nil
}

// updateFailoverStatus updates the status of the Failover CR
func (m *Manager) updateFailoverStatus(ctx context.Context,
	failover *crdv1alpha1.Failover,
	status string,
	errors []error) error {

	failover.Status.Status = status

	// If errors occurred, add them to the conditions
	if len(errors) > 0 {
		errorMessages := ""
		for _, err := range errors {
			errorMessages += err.Error() + "; "
		}

		failover.Status.Conditions = append(failover.Status.Conditions, metav1.Condition{
			Type:               "Failed",
			Status:             metav1.ConditionTrue,
			LastTransitionTime: metav1.Now(),
			Reason:             "FailoverErrors",
			Message:            errorMessages,
		})
	} else if status == "SUCCESS" {
		// Add a success condition
		failover.Status.Conditions = append(failover.Status.Conditions, metav1.Condition{
			Type:               "Succeeded",
			Status:             metav1.ConditionTrue,
			LastTransitionTime: metav1.Now(),
			Reason:             "FailoverCompleted",
			Message:            "Failover completed successfully",
		})
	}

	return m.Client.Status().Update(ctx, failover)
}

// StartAutomaticFailoverChecker starts a background goroutine that periodically checks
// for automatic failover triggers. The check interval is specified in seconds.
func (m *Manager) StartAutomaticFailoverChecker(ctx context.Context, intervalSeconds int) {
	if intervalSeconds <= 0 {
		intervalSeconds = 60 // Default to 60 seconds if no valid interval is provided
	}

	go func() {
		ticker := time.NewTicker(time.Duration(intervalSeconds) * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := m.CheckAutomaticFailoverTriggers(ctx); err != nil {
					m.Log.Error(err, "Failed to check automatic failover triggers")
				}
			}
		}
	}()

	m.Log.Info("Started automatic failover checker", "intervalSeconds", intervalSeconds)
}

// CleanupFailoverResources cleans up any resources that might have been left in
// an inconsistent state after a failed failover
func (m *Manager) CleanupFailoverResources(ctx context.Context, failover *crdv1alpha1.Failover) error {
	log := m.Log.WithValues("failover", failover.Name, "namespace", failover.Namespace)
	log.Info("Cleaning up failover resources")

	// For each failover group
	for _, group := range failover.Spec.FailoverGroups {
		failoverGroup := &crdv1alpha1.FailoverGroup{}
		groupNamespace := group.Namespace
		if groupNamespace == "" {
			groupNamespace = failover.Namespace
		}

		// Try to get the failover group
		if err := m.Client.Get(ctx, client.ObjectKey{
			Namespace: groupNamespace,
			Name:      group.Name,
		}, failoverGroup); err != nil {
			log.Error(err, "Failed to get failover group during cleanup")
			continue
		}

		// Release any locks the failover might have acquired
		if err := m.releaseLock(ctx, failoverGroup); err != nil {
			log.Error(err, "Failed to release lock during cleanup")
		}

		// Update status to indicate cleanup has happened
		// This is important for the operator's observability
		failoverGroup.Status.Conditions = append(failoverGroup.Status.Conditions, metav1.Condition{
			Type:               "CleanedUp",
			Status:             metav1.ConditionTrue,
			LastTransitionTime: metav1.Now(),
			Reason:             "FailoverResourceDeleted",
			Message:            "Resources cleaned up due to failover resource deletion",
		})

		if err := m.Client.Status().Update(ctx, failoverGroup); err != nil {
			log.Error(err, "Failed to update failover group status during cleanup")
		}
	}

	return nil
}
