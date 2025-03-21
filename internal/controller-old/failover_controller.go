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

package controller

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	crdv1alpha1 "github.com/christensenjairus/Failover-Operator/api/v1alpha1"
	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Constants for annotations
const (
	// FluxReconcileAnnotation is the annotation used to control whether Flux reconciles a resource
	FluxReconcileAnnotation = "kustomize.toolkit.fluxcd.io/reconcile"
	// DisabledValue is the value to set for disabling Flux reconciliation
	DisabledValue = "disabled"
)

// FailoverReconciler reconciles a Failover object
type FailoverReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Log         logr.Logger
	ClusterName string // Current cluster's name
}

//+kubebuilder:rbac:groups=crd.hahomelabs.com,resources=failovers,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=crd.hahomelabs.com,resources=failovers/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=crd.hahomelabs.com,resources=failovers/finalizers,verbs=update
//+kubebuilder:rbac:groups=crd.hahomelabs.com,resources=failovergroups,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=apps,resources=deployments;statefulsets,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=batch,resources=cronjobs,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=networking.istio.io,resources=virtualservices,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=helm.toolkit.fluxcd.io,resources=helmreleases,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=kustomize.toolkit.fluxcd.io,resources=kustomizations,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=replication.storage.openshift.io,resources=volumereplications,verbs=get;list;watch;update;patch

// Reconcile handles the main reconciliation logic for Failover
func (r *FailoverReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("failover", req.NamespacedName)
	r.Log = logger

	// Fetch the Failover instance
	failover := &crdv1alpha1.Failover{}
	err := r.Get(ctx, req.NamespacedName, failover)
	if err != nil {
		if errors.IsNotFound(err) {
			// Object not found, might have been deleted
			return ctrl.Result{}, nil
		}
		// Error reading the object
		logger.Error(err, "Failed to get Failover")
		return ctrl.Result{}, err
	}

	// Add finalizer if it doesn't exist
	if !controllerutil.ContainsFinalizer(failover, "failover.hahomelabs.com/finalizer") {
		controllerutil.AddFinalizer(failover, "failover.hahomelabs.com/finalizer")
		if err := r.Update(ctx, failover); err != nil {
			logger.Error(err, "Failed to add finalizer")
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Handle deletion if marked for deletion
	if !failover.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, failover)
	}

	// Check if this is an emergency operation and log appropriately
	isEmergency := failover.Spec.Type == "emergency"
	if isEmergency {
		// Check for 'failback-for' annotation to get the original failover name
		if originalFailover, ok := failover.Annotations["failover.hahomelabs.com/failback-for"]; ok {
			logger.Info("Processing emergency failback",
				"originalFailover", originalFailover,
				"reason", failover.Annotations["failover.hahomelabs.com/failure-reason"])
		} else {
			logger.Info("Processing emergency failover")
		}
	}

	// Initialize status if it's empty
	if failover.Status.Status == "" {
		// Before initializing, check if there are any other IN_PROGRESS failovers
		// Emergency failovers can proceed regardless of other failovers in progress
		if !isEmergency {
			otherInProgress, err := r.isAnyFailoverInProgress(ctx, failover)
			if err != nil {
				logger.Error(err, "Failed to check for in-progress failovers")
				return ctrl.Result{}, err
			}

			if otherInProgress {
				// There's another failover in progress - add a condition to indicate we're waiting
				logger.Info("Another failover is in progress, waiting for it to complete")

				waitingCondition := metav1.Condition{
					Type:               "Waiting",
					Status:             metav1.ConditionTrue,
					LastTransitionTime: metav1.Now(),
					Reason:             "AnotherFailoverInProgress",
					Message:            "Waiting for another failover to complete before starting",
				}

				// Only update the status if we haven't already set the Waiting condition
				hasWaitingCondition := false
				for _, cond := range failover.Status.Conditions {
					if cond.Type == "Waiting" && cond.Status == metav1.ConditionTrue {
						hasWaitingCondition = true
						break
					}
				}

				if !hasWaitingCondition {
					meta.SetStatusCondition(&failover.Status.Conditions, waitingCondition)
					if err := r.Status().Update(ctx, failover); err != nil {
						logger.Error(err, "Failed to update Failover waiting status")
						return ctrl.Result{}, err
					}
				}

				// Requeue to check again later
				return ctrl.Result{RequeueAfter: time.Second * 10}, nil
			}
		}

		logger.Info("Initializing Failover status", "isEmergency", isEmergency)
		failover.Status.Status = "IN_PROGRESS"
		failover.Status.Metrics = crdv1alpha1.FailoverMetrics{}

		// Initialize status for each FailoverGroup
		failover.Status.FailoverGroups = make([]crdv1alpha1.FailoverGroupReference, 0, len(failover.Spec.FailoverGroups))
		for _, groupRef := range failover.Spec.FailoverGroups {
			newRef := groupRef.DeepCopy()
			newRef.Status = "IN_PROGRESS"
			newRef.StartTime = time.Now().Format(time.RFC3339)
			failover.Status.FailoverGroups = append(failover.Status.FailoverGroups, *newRef)
		}

		// Set the overall failover start time
		startCondition := metav1.Condition{
			Type:               "Started",
			Status:             metav1.ConditionTrue,
			LastTransitionTime: metav1.Now(),
			Reason:             "FailoverStarted",
			Message:            "Failover operation has started",
		}
		meta.SetStatusCondition(&failover.Status.Conditions, startCondition)

		// If this is an emergency operation, add a special condition
		if isEmergency {
			emergencyCondition := metav1.Condition{
				Type:               "Emergency",
				Status:             metav1.ConditionTrue,
				LastTransitionTime: metav1.Now(),
				Reason:             "EmergencyOperation",
				Message:            "This is an emergency operation to quickly restore service",
			}
			meta.SetStatusCondition(&failover.Status.Conditions, emergencyCondition)
		}

		if err := r.Status().Update(ctx, failover); err != nil {
			logger.Error(err, "Failed to initialize Failover status")
			return ctrl.Result{}, err
		}

		// Requeue to continue processing after status is initialized
		return ctrl.Result{Requeue: true}, nil
	}

	// Only process if status is IN_PROGRESS
	if failover.Status.Status == "IN_PROGRESS" {
		startTime := getFailoverStartTime(failover.Status.Conditions)

		// Check if failover has been running for too long (potential deadlock)
		if startTime != nil {
			currentTime := time.Now()
			failoverDuration := currentTime.Sub(*startTime)
			maxFailoverTime := 30 * time.Minute // Maximum allowed time for failover

			if failoverDuration > maxFailoverTime {
				logger.Info("Failover has exceeded maximum allowed time, marking as failed",
					"duration", failoverDuration.String(), "maxAllowed", maxFailoverTime.String())

				previousState := failover.Status.Status
				failover.Status.Status = "FAILED"
				failedCondition := metav1.Condition{
					Type:               "Failed",
					Status:             metav1.ConditionTrue,
					LastTransitionTime: metav1.Now(),
					Reason:             "FailoverTimeout",
					Message:            "Failover operation timed out after " + failoverDuration.String(),
				}
				meta.SetStatusCondition(&failover.Status.Conditions, failedCondition)
				if err := r.Status().Update(ctx, failover); err != nil {
					logger.Error(err, "Failed to update Failover status")
					return ctrl.Result{}, err
				}

				// If this failover has just transitioned to FAILED state due to timeout,
				// process any waiting failovers to allow the next one to start
				if previousState == "IN_PROGRESS" {
					logger.Info("Failover has timed out, processing waiting failovers queue")
					if err := r.processWaitingFailovers(ctx); err != nil {
						logger.Error(err, "Failed to process waiting failovers")
						// Don't return an error, as this is a non-critical operation
					}
				}

				// Trigger recovery
				return r.initiateEmergencyFailback(ctx, failover)
			}
		}

		// Process each FailoverGroup
		completed := true
		allSuccess := true
		hasFailedGroups := false

		for i, groupRef := range failover.Status.FailoverGroups {
			if groupRef.Status != "IN_PROGRESS" {
				// Skip groups that are already completed
				if groupRef.Status == "FAILED" {
					hasFailedGroups = true
				}
				continue
			}

			// Get the FailoverGroup
			failoverGroup := &crdv1alpha1.FailoverGroup{}
			groupNamespacedName := types.NamespacedName{
				Name:      groupRef.Name,
				Namespace: groupRef.Namespace,
			}
			if groupRef.Namespace == "" {
				groupNamespacedName.Namespace = failover.Namespace
			}

			err := r.Get(ctx, groupNamespacedName, failoverGroup)
			if err != nil {
				if errors.IsNotFound(err) {
					logger.Error(err, "FailoverGroup not found", "group", groupNamespacedName)
					failover.Status.FailoverGroups[i].Status = "FAILED"
					failover.Status.FailoverGroups[i].Message = fmt.Sprintf("FailoverGroup %s not found", groupRef.Name)
					allSuccess = false
					hasFailedGroups = true
				} else {
					logger.Error(err, "Failed to get FailoverGroup", "group", groupNamespacedName)
					return ctrl.Result{}, err
				}
			} else {
				// Process the failover for this group
				groupStatus, err := r.processGroupFailover(ctx, failover, failoverGroup)
				if err != nil {
					logger.Error(err, "Failed to process group failover", "group", groupNamespacedName)
					failover.Status.FailoverGroups[i].Status = "FAILED"
					failover.Status.FailoverGroups[i].Message = fmt.Sprintf("Failover processing error: %v", err)
					allSuccess = false
					hasFailedGroups = true
				} else if groupStatus == "IN_PROGRESS" {
					completed = false
				} else {
					failover.Status.FailoverGroups[i].Status = groupStatus
					failover.Status.FailoverGroups[i].CompletionTime = time.Now().Format(time.RFC3339)

					if groupStatus == "FAILED" {
						allSuccess = false
						hasFailedGroups = true
						failover.Status.FailoverGroups[i].Message = "Failover operation failed for this group"
					} else {
						failover.Status.FailoverGroups[i].Message = "Failover completed successfully"
					}
				}
			}
		}

		// If some groups failed but we're still in progress, check if we need to initiate recovery
		if hasFailedGroups && !completed {
			// Check if we have too many failed groups that would make the overall failover impossible
			failedCount := 0
			totalCount := len(failover.Status.FailoverGroups)

			for _, groupRef := range failover.Status.FailoverGroups {
				if groupRef.Status == "FAILED" {
					failedCount++
				}
			}

			// If more than half the groups failed, initiate recovery
			if failedCount > totalCount/2 {
				logger.Info("Too many FailoverGroups have failed, initiating recovery",
					"failed", failedCount, "total", totalCount)

				previousState := failover.Status.Status
				failover.Status.Status = "FAILED"
				failedCondition := metav1.Condition{
					Type:               "Failed",
					Status:             metav1.ConditionTrue,
					LastTransitionTime: metav1.Now(),
					Reason:             "TooManyFailedGroups",
					Message:            fmt.Sprintf("%d of %d FailoverGroups failed, initiating recovery", failedCount, totalCount),
				}
				meta.SetStatusCondition(&failover.Status.Conditions, failedCondition)
				if err := r.Status().Update(ctx, failover); err != nil {
					logger.Error(err, "Failed to update Failover status")
					return ctrl.Result{}, err
				}

				// If this failover has just transitioned to FAILED state,
				// process any waiting failovers to allow the next one to start
				if previousState == "IN_PROGRESS" {
					logger.Info("Failover has failed due to too many failed groups, processing waiting failovers queue")
					if err := r.processWaitingFailovers(ctx); err != nil {
						logger.Error(err, "Failed to process waiting failovers")
						// Don't return an error, as this is a non-critical operation
					}
				}

				// Trigger recovery
				return r.initiateEmergencyFailback(ctx, failover)
			}
		}

		// Update overall status if all groups are processed
		if completed {
			endTime := time.Now()
			if startTime != nil {
				failover.Status.Metrics.TotalFailoverTimeSeconds = int64(endTime.Sub(*startTime).Seconds())
			}

			previousState := failover.Status.Status
			if allSuccess {
				failover.Status.Status = "SUCCESS"
				completedCondition := metav1.Condition{
					Type:               "Completed",
					Status:             metav1.ConditionTrue,
					LastTransitionTime: metav1.Now(),
					Reason:             "FailoverCompleted",
					Message:            "Failover operation completed successfully",
				}
				meta.SetStatusCondition(&failover.Status.Conditions, completedCondition)
			} else {
				failover.Status.Status = "FAILED"
				failedCondition := metav1.Condition{
					Type:               "Failed",
					Status:             metav1.ConditionTrue,
					LastTransitionTime: metav1.Now(),
					Reason:             "FailoverFailed",
					Message:            "Failover operation failed for some groups",
				}
				meta.SetStatusCondition(&failover.Status.Conditions, failedCondition)

				// Since we've completed with failure, initiate recovery
				if err := r.Status().Update(ctx, failover); err != nil {
					logger.Error(err, "Failed to update Failover status")
					return ctrl.Result{}, err
				}
				return r.initiateEmergencyFailback(ctx, failover)
			}

			if err := r.Status().Update(ctx, failover); err != nil {
				logger.Error(err, "Failed to update Failover status")
				return ctrl.Result{}, err
			}

			// If this failover has just transitioned to a completed state (SUCCESS or FAILED),
			// process any waiting failovers to allow the next one to start
			if previousState == "IN_PROGRESS" {
				logger.Info("Failover has completed, processing waiting failovers queue")
				if err := r.processWaitingFailovers(ctx); err != nil {
					logger.Error(err, "Failed to process waiting failovers")
					// Don't return an error, as this is a non-critical operation
				}
			}

			// No need to requeue after completion
			return ctrl.Result{}, nil
		}

		// Update status and requeue for in-progress operations
		if err := r.Status().Update(ctx, failover); err != nil {
			logger.Error(err, "Failed to update Failover status")
			return ctrl.Result{}, err
		}

		// Requeue with a shorter interval for in-progress operations
		return ctrl.Result{RequeueAfter: time.Second * 10}, nil
	}

	// For already completed failovers, check less frequently
	return ctrl.Result{RequeueAfter: time.Minute * 30}, nil
}

// updateFailoverGroupWithRetry updates a FailoverGroup's status with retries to handle conflicts
func (r *FailoverReconciler) updateFailoverGroupWithRetry(ctx context.Context, group *crdv1alpha1.FailoverGroup) error {
	logger := r.Log.WithValues("group", fmt.Sprintf("%s/%s", group.Namespace, group.Name))
	maxRetries := 5
	retryDelay := 200 * time.Millisecond

	for i := 0; i < maxRetries; i++ {
		err := r.Status().Update(ctx, group)
		if err == nil {
			return nil // Success
		}

		// Check if it's a conflict error
		if errors.IsConflict(err) {
			logger.Info("Conflict updating FailoverGroup status, retrying",
				"attempt", i+1, "maxRetries", maxRetries)

			// Get the latest version
			latestGroup := &crdv1alpha1.FailoverGroup{}
			if getErr := r.Get(ctx, types.NamespacedName{
				Namespace: group.Namespace,
				Name:      group.Name,
			}, latestGroup); getErr != nil {
				logger.Error(getErr, "Failed to get latest FailoverGroup version")
				return getErr
			}

			// Preserve the important fields we wanted to update
			latestGroup.Status.State = group.Status.State
			latestGroup.Status.LastFailoverTime = group.Status.LastFailoverTime

			// Use the latest version for the next attempt
			group = latestGroup

			// Wait before retry
			time.Sleep(retryDelay)
			// Increase delay for next potential retry (exponential backoff)
			retryDelay = retryDelay * 2
			continue
		}

		// Not a conflict error, return it
		return err
	}

	return fmt.Errorf("failed to update FailoverGroup status after %d retries", maxRetries)
}

// processGroupFailover handles the failover logic for a specific FailoverGroup
func (r *FailoverReconciler) processGroupFailover(ctx context.Context, failover *crdv1alpha1.Failover, group *crdv1alpha1.FailoverGroup) (string, error) {
	logger := r.Log.WithValues("group", fmt.Sprintf("%s/%s", group.Namespace, group.Name))

	// Get the current cluster name
	logger.Info("Processing failover",
		"targetCluster", failover.Spec.TargetCluster,
		"currentCluster", r.ClusterName,
		"currentState", group.Status.State,
		"isEmergency", failover.Spec.Type == "emergency")

	// Check if this is an emergency operation
	isEmergency := failover.Spec.Type == "emergency"

	// Check if this is a failover to this cluster or away from this cluster
	isTargetingThisCluster := failover.Spec.TargetCluster == r.ClusterName

	// For emergency operations, we need to be more aggressive in forcing the state changes
	if isEmergency {
		logger.Info("This is an emergency operation, handling with special care")

		if isTargetingThisCluster {
			// This is an emergency to make THIS cluster PRIMARY
			logger.Info("Emergency operation: Setting this cluster to PRIMARY")

			// Force state to FAILBACK and then to PRIMARY
			group.Status.State = string(crdv1alpha1.FailoverGroupStateFailback)
			if err := r.updateFailoverGroupWithRetry(ctx, group); err != nil {
				logger.Error(err, "Failed to update FailoverGroup state during emergency")
				return "FAILED", err
			}

			// For emergency, immediately resume Flux resources if defined
			if len(group.Spec.ParentFluxResources) > 0 {
				logger.Info("Emergency: Immediately resuming Flux resources")
				if err := r.resumeFluxResources(ctx, group.Spec.ParentFluxResources, group.Namespace); err != nil {
					logger.Error(err, "Failed to resume Flux resources during emergency")
					// Continue despite error for emergency
				}

				// Force trigger Flux reconciliation
				if err := r.triggerFluxReconciliation(ctx, group.Spec.ParentFluxResources, group.Namespace); err != nil {
					logger.Error(err, "Failed to trigger Flux reconciliation during emergency")
					// Continue despite error for emergency
				}
			}

			// Remove Flux annotations for all workloads to allow Flux to take over
			for _, comp := range group.Spec.Components {
				for _, workload := range comp.Workloads {
					if err := r.removeFluxReconcileAnnotation(ctx, workload.Kind, workload.Name, group.Namespace); err != nil {
						logger.Error(err, "Failed to remove Flux reconcile annotation during emergency",
							"kind", workload.Kind, "name", workload.Name)
						// Continue despite error for emergency
					}
				}
			}

			// Process volume replications immediately for emergency
			for _, comp := range group.Spec.Components {
				if len(comp.VolumeReplications) > 0 {
					logger.Info("Emergency: Promoting volume replications to primary state",
						"component", comp.Name,
						"volumeReplicationCount", len(comp.VolumeReplications),
						"targetState", "primary",
						"mode", "emergency")

					// Process each volume replication to set to primary
					for _, volRep := range comp.VolumeReplications {
						logger.Info("Emergency: Setting volume replication to primary",
							"component", comp.Name,
							"volumeReplication", volRep,
							"currentState", group.Status.State,
							"targetState", "primary")

						if err := r.setVolumeReplicationState(ctx, volRep, group.Namespace, "primary"); err != nil {
							logger.Error(err, "Failed to promote volume replication in emergency",
								"component", comp.Name,
								"volumeReplication", volRep)
							// Continue despite errors
						} else {
							logger.Info("Emergency: Successfully requested primary state for volume replication",
								"component", comp.Name,
								"volumeReplication", volRep)
						}
					}
				}
			}

			// Check workload status for emergency operations
			workloadCount := 0
			allWorkloadsReady := true

			// Check if workloads are ready, but don't block for emergency operations
			for _, comp := range group.Spec.Components {
				for _, workload := range comp.Workloads {
					workloadCount++
					isReady, err := r.checkWorkloadReady(ctx, workload.Kind, workload.Name, group.Namespace)
					if err != nil {
						logger.Error(err, "Failed to check workload status during emergency",
							"kind", workload.Kind, "name", workload.Name)
						allWorkloadsReady = false
					} else if !isReady {
						logger.Info("Workload not ready during emergency operation",
							"kind", workload.Kind, "name", workload.Name)
						allWorkloadsReady = false
					}
				}
			}

			// Log workload status but proceed with emergency
			if workloadCount > 0 && !allWorkloadsReady {
				logger.Info("Some workloads are not ready yet, but proceeding with emergency operation",
					"workloadCount", workloadCount)
			}

			// This is necessary for emergency to break potential deadlocks
			logger.Info("Emergency: Setting state to PRIMARY immediately to break potential deadlocks")

			group.Status.State = string(crdv1alpha1.FailoverGroupStatePrimary)
			group.Status.LastFailoverTime = time.Now().Format(time.RFC3339)

			if err := r.updateFailoverGroupWithRetry(ctx, group); err != nil {
				logger.Error(err, "Failed to update FailoverGroup state for emergency")
				return "FAILED", err
			}

			// For emergency operations, we want to proceed even if the workloads are not ready
			// But we should return IN_PROGRESS to requeue and check again later if workloads aren't ready yet
			if workloadCount > 0 && !allWorkloadsReady {
				logger.Info("Emergency operation proceeding, but workloads not yet ready - will check again")
				return "IN_PROGRESS", nil
			}

			// Return success if workloads are ready or no workloads to check
			return "SUCCESS", nil

		} else {
			// This is an emergency to make THIS cluster STANDBY
			logger.Info("Emergency operation: Setting this cluster to STANDBY")

			// For emergency, set state to FAILOVER and then immediately to STANDBY
			group.Status.State = string(crdv1alpha1.FailoverGroupStateFailover)
			if err := r.updateFailoverGroupWithRetry(ctx, group); err != nil {
				logger.Error(err, "Failed to update FailoverGroup state during emergency")
				return "FAILED", err
			}

			// Immediately suspend Flux resources if defined
			if len(group.Spec.ParentFluxResources) > 0 {
				logger.Info("Emergency: Immediately suspending Flux resources")
				if err := r.suspendFluxResources(ctx, group.Spec.ParentFluxResources, group.Namespace); err != nil {
					logger.Error(err, "Failed to suspend Flux resources during emergency")
					// Continue despite error for emergency
				}
			}

			// Add Flux annotations for all workloads
			for _, comp := range group.Spec.Components {
				for _, workload := range comp.Workloads {
					if err := r.addFluxReconcileAnnotation(ctx, workload.Kind, workload.Name, group.Namespace); err != nil {
						logger.Error(err, "Failed to add Flux reconcile annotation during emergency",
							"kind", workload.Kind, "name", workload.Name)
						// Continue despite error for emergency
					}

					// Force scale down workloads immediately for emergency
					if err := r.scaleWorkload(ctx, workload.Kind, workload.Name, group.Namespace, 0); err != nil {
						logger.Error(err, "Failed to scale down workload during emergency",
							"kind", workload.Kind, "name", workload.Name)
						// Continue despite error for emergency
					}
				}
			}

			// Process volume replications immediately for emergency
			for _, comp := range group.Spec.Components {
				if len(comp.VolumeReplications) > 0 {
					logger.Info("Emergency: Demoting volume replications to secondary state",
						"component", comp.Name,
						"volumeReplicationCount", len(comp.VolumeReplications),
						"targetState", "secondary",
						"mode", "emergency")

					// Process each volume replication to set to secondary
					for _, volRep := range comp.VolumeReplications {
						logger.Info("Emergency: Setting volume replication to secondary",
							"component", comp.Name,
							"volumeReplication", volRep,
							"currentState", group.Status.State,
							"targetState", "secondary")

						if err := r.setVolumeReplicationState(ctx, volRep, group.Namespace, "secondary"); err != nil {
							logger.Error(err, "Failed to demote volume replication in emergency",
								"component", comp.Name,
								"volumeReplication", volRep)
							// Continue despite errors
						} else {
							logger.Info("Emergency: Successfully requested secondary state for volume replication",
								"component", comp.Name,
								"volumeReplication", volRep)
						}
					}
				}
			}

			// This is necessary for emergency to break potential deadlocks
			logger.Info("Emergency: Setting state to STANDBY immediately to break potential deadlocks")
			group.Status.State = string(crdv1alpha1.FailoverGroupStateStandby)
			group.Status.LastFailoverTime = time.Now().Format(time.RFC3339)

			if err := r.updateFailoverGroupWithRetry(ctx, group); err != nil {
				logger.Error(err, "Failed to update FailoverGroup state for emergency")
				return "FAILED", err
			}

			// Return success immediately to prevent further processing
			return "SUCCESS", nil
		}
	}

	// REMOVED: The checks that skip failover when already in desired state
	// Always proceed with failover process regardless of current state
	// This helps clean drift and forces reconciliation
	logger.Info("Always proceeding with failover to ensure state consistency and reconciliation")

	// Set the appropriate transitory state if we're just starting the failover process
	if group.Status.State != string(crdv1alpha1.FailoverGroupStateFailback) &&
		group.Status.State != string(crdv1alpha1.FailoverGroupStateFailover) {

		// Determine the appropriate transitory state based on the relationship between
		// the current cluster and the target cluster in the Failover CR
		var transitionState string
		if isTargetingThisCluster {
			// This cluster is becoming PRIMARY (targeted by the failover) - it's a FAILBACK
			transitionState = string(crdv1alpha1.FailoverGroupStateFailback)
			logger.Info("Setting transitory state to FAILOVER (becoming PRIMARY)")
		} else {
			// This cluster is becoming STANDBY (not targeted by the failover) - it's a FAILOVER
			transitionState = string(crdv1alpha1.FailoverGroupStateFailover)
			logger.Info("Setting transitory state to FAILBACK (becoming STANDBY)")
		}

		group.Status.State = transitionState
		if err := r.updateFailoverGroupWithRetry(ctx, group); err != nil {
			logger.Error(err, "Failed to update FailoverGroup to transitory state",
				"state", transitionState)
			return "FAILED", err
		}
		// Return IN_PROGRESS to requeue and continue processing
		return "IN_PROGRESS", nil
	}

	// If target cluster is this cluster, make it PRIMARY
	if isTargetingThisCluster {
		// Verify we're in the FAILOVER state (transitioning to PRIMARY)
		if group.Status.State != string(crdv1alpha1.FailoverGroupStateFailback) {
			logger.Info("Group not in FAILOVER state, setting it now",
				"currentState", group.Status.State)
			group.Status.State = string(crdv1alpha1.FailoverGroupStateFailback)
			if err := r.updateFailoverGroupWithRetry(ctx, group); err != nil {
				logger.Error(err, "Failed to update FailoverGroup state")
				return "FAILED", err
			}
			return "IN_PROGRESS", nil
		}

		// First resume flux resources if defined
		if len(group.Spec.ParentFluxResources) > 0 {
			logger.Info("Resuming Flux resources for PRIMARY transition",
				"resourceCount", len(group.Spec.ParentFluxResources))
			if err := r.resumeFluxResources(ctx, group.Spec.ParentFluxResources, group.Namespace); err != nil {
				logger.Error(err, "Failed to resume Flux resources")
				return "FAILED", err
			}
		}

		// Process volume replications to promote them if necessary
		for _, comp := range group.Spec.Components {
			if len(comp.VolumeReplications) > 0 {
				// Process volume replications
				logger.Info("Processing volume replications for PRIMARY transition",
					"component", comp.Name,
					"volumeReplicationCount", len(comp.VolumeReplications),
					"targetState", "primary")

				// Process each volume replication to set it to primary (read-write) mode
				for _, volRep := range comp.VolumeReplications {
					logger.Info("Setting volume replication to primary",
						"component", comp.Name,
						"volumeReplication", volRep,
						"currentState", group.Status.State,
						"targetState", "primary")

					// Call the volume replication state change method
					if err := r.setVolumeReplicationState(ctx, volRep, group.Namespace, "primary"); err != nil {
						logger.Error(err, "Failed to set volume replication to primary",
							"component", comp.Name,
							"volumeReplication", volRep)
						// Continue despite errors to try to set all volume replications
					} else {
						logger.Info("Successfully requested primary state for volume replication",
							"component", comp.Name,
							"volumeReplication", volRep)
					}
				}
			}
		}

		// Remove the Flux reconcile annotation from all workloads
		// This will allow Flux to reconcile and scale up the workloads
		for _, comp := range group.Spec.Components {
			for _, workload := range comp.Workloads {
				if err := r.removeFluxReconcileAnnotation(ctx, workload.Kind, workload.Name, group.Namespace); err != nil {
					logger.Error(err, "Failed to remove Flux reconcile annotation",
						"kind", workload.Kind, "name", workload.Name)
					// Log but continue, as this is not critical to the failover process
				}
			}
		}

		// Trigger reconciliation of Flux resources to speed up scaling up
		if len(group.Spec.ParentFluxResources) > 0 {
			logger.Info("Triggering Flux resource reconciliation")
			if err := r.triggerFluxReconciliation(ctx, group.Spec.ParentFluxResources, group.Namespace); err != nil {
				logger.Error(err, "Failed to trigger Flux reconciliation")
				// Continue despite errors, as this is just to speed up the process
			}
		}

		// Verify that workloads are scaled up before marking as PRIMARY
		allWorkloadsReady := true
		workloadCount := 0
		for _, comp := range group.Spec.Components {
			for _, workload := range comp.Workloads {
				workloadCount++
				isReady, err := r.checkWorkloadReady(ctx, workload.Kind, workload.Name, group.Namespace)
				if err != nil {
					logger.Error(err, "Failed to check workload status",
						"kind", workload.Kind, "name", workload.Name)
					allWorkloadsReady = false
					break
				}

				if !isReady {
					logger.Info("Workload not ready yet, waiting for Flux to scale it up",
						"kind", workload.Kind, "name", workload.Name)
					allWorkloadsReady = false
					break
				}
			}
			if !allWorkloadsReady {
				break
			}
		}

		if !allWorkloadsReady {
			// If workloads are not ready, return IN_PROGRESS to trigger another reconciliation
			logger.Info("Workloads not ready yet, waiting for Flux to complete reconciliation")
			// Only update to PRIMARY state after workloads are ready
			return "IN_PROGRESS", nil
		}

		// Update virtual services to point to this cluster
		for _, comp := range group.Spec.Components {
			if len(comp.VirtualServices) > 0 {
				logger.Info("Updating virtual services for component", "component", comp.Name)
				// In production code, this would enable the virtual services
			}
		}

		// Don't change state to PRIMARY unless all workloads are actually ready
		if workloadCount > 0 && allWorkloadsReady {
			logger.Info("All workloads are ready, setting group to PRIMARY state")
			// Record the time of the failover completion
			group.Status.LastFailoverTime = time.Now().Format(time.RFC3339)
			// Only after all checks have passed, update the state to PRIMARY
			group.Status.State = string(crdv1alpha1.FailoverGroupStatePrimary)
		} else if workloadCount == 0 {
			// No workloads to check, just set to PRIMARY
			logger.Info("No workloads to check, setting group to PRIMARY state")
			// Record the time of the failover completion
			group.Status.LastFailoverTime = time.Now().Format(time.RFC3339)
			// Update the state to PRIMARY
			group.Status.State = string(crdv1alpha1.FailoverGroupStatePrimary)
		} else {
			// This should not happen given our checks above, but just in case
			logger.Info("Workloads not ready, keeping state as FAILBACK")
			return "IN_PROGRESS", nil
		}
	} else {
		// Verify we're in the FAILOVER state (transitioning to STANDBY)
		if group.Status.State != string(crdv1alpha1.FailoverGroupStateFailover) {
			logger.Info("Group not in FAILBACK state, setting it now",
				"currentState", group.Status.State)
			group.Status.State = string(crdv1alpha1.FailoverGroupStateFailover)
			if err := r.updateFailoverGroupWithRetry(ctx, group); err != nil {
				logger.Error(err, "Failed to update FailoverGroup state")
				return "FAILED", err
			}
			return "IN_PROGRESS", nil
		}

		// If target cluster is different, make this cluster STANDBY
		logger.Info("Setting group to STANDBY state")

		// Check safe mode setting for components with volume replications
		safeMode := group.Spec.DefaultFailoverMode == "safe"

		// First add Flux reconcile=disabled annotations to all workloads
		// This prevents Flux from re-scaling them during the scale-down process
		for _, comp := range group.Spec.Components {
			// Check component-specific failover mode if set
			componentSafeMode := safeMode
			if comp.FailoverMode != "" {
				componentSafeMode = comp.FailoverMode == "safe"
			}

			for _, workload := range comp.Workloads {
				logger.Info("Adding Flux reconcile=disabled annotation",
					"kind", workload.Kind, "name", workload.Name)
				// Add the Flux reconcile annotation to prevent Flux from reconciling this resource
				if err := r.addFluxReconcileAnnotation(ctx, workload.Kind, workload.Name, group.Namespace); err != nil {
					logger.Error(err, "Failed to add Flux reconcile annotation",
						"kind", workload.Kind, "name", workload.Name)
					// Log but continue
				}
			}

			// For components in safe mode with volume replications, we need to:
			// 1. Scale down workloads completely
			// 2. Then demote volume replications to secondary
			if componentSafeMode && len(comp.VolumeReplications) > 0 {
				logger.Info("Processing component in safe mode",
					"component", comp.Name,
					"volumeReplicationCount", len(comp.VolumeReplications))

				// Scale down all workloads first
				for _, workload := range comp.Workloads {
					if err := r.scaleWorkload(ctx, workload.Kind, workload.Name, group.Namespace, 0); err != nil {
						logger.Error(err, "Failed to scale down workload in safe mode",
							"kind", workload.Kind, "name", workload.Name)
						return "FAILED", err
					}
				}

				// Verify all workloads are fully scaled down
				allScaledDown := true
				for _, workload := range comp.Workloads {
					isDown, err := r.isWorkloadScaledDown(ctx, workload.Kind, workload.Name, group.Namespace)
					if err != nil {
						logger.Error(err, "Failed to check if workload is scaled down",
							"kind", workload.Kind, "name", workload.Name)
						return "FAILED", err
					}

					if !isDown {
						allScaledDown = false
						logger.Info("Waiting for workload to scale down completely",
							"kind", workload.Kind, "name", workload.Name)
						break
					}
				}

				if !allScaledDown {
					// Not all workloads are scaled down yet, return IN_PROGRESS
					logger.Info("Waiting for all workloads to scale down before demoting volume replications")
					return "IN_PROGRESS", nil
				}

				// Now that all workloads are scaled down, demote volume replications
				logger.Info("All workloads scaled down, demoting volume replications",
					"component", comp.Name,
					"volumeReplicationCount", len(comp.VolumeReplications),
					"targetState", "secondary")

				// Process each volume replication to set it to secondary (read-only) mode
				for _, volRep := range comp.VolumeReplications {
					logger.Info("Setting volume replication to secondary",
						"component", comp.Name,
						"volumeReplication", volRep,
						"currentState", group.Status.State,
						"targetState", "secondary")

					// Call the same method we use for safe mode
					if err := r.setVolumeReplicationState(ctx, volRep, group.Namespace, "secondary"); err != nil {
						logger.Error(err, "Failed to set volume replication to secondary",
							"component", comp.Name,
							"volumeReplication", volRep)
						// Continue despite errors to try to set all volume replications
					} else {
						logger.Info("Successfully requested secondary state for volume replication",
							"component", comp.Name,
							"volumeReplication", volRep)
					}
				}
			}
		}

		// For components not in safe mode or without volume replications,
		// we can scale down all workloads in parallel
		for _, comp := range group.Spec.Components {
			componentSafeMode := safeMode
			if comp.FailoverMode != "" {
				componentSafeMode = comp.FailoverMode == "safe"
			}

			// Skip components we already processed in safe mode above
			if componentSafeMode && len(comp.VolumeReplications) > 0 {
				continue
			}

			// Scale down workloads
			for _, workload := range comp.Workloads {
				logger.Info("Scaling down workload", "kind", workload.Kind, "name", workload.Name)
				if err := r.scaleWorkload(ctx, workload.Kind, workload.Name, group.Namespace, 0); err != nil {
					logger.Error(err, "Failed to scale down workload",
						"kind", workload.Kind, "name", workload.Name)
					return "FAILED", err
				}
			}

			// Demote volume replications immediately for non-safe mode
			if !componentSafeMode && len(comp.VolumeReplications) > 0 {
				logger.Info("Demoting volume replications in fast mode",
					"component", comp.Name,
					"volumeReplicationCount", len(comp.VolumeReplications),
					"targetState", "secondary")

				// Process each volume replication to set it to secondary (read-only) mode
				for _, volRep := range comp.VolumeReplications {
					logger.Info("Setting volume replication to secondary (fast mode)",
						"component", comp.Name,
						"volumeReplication", volRep,
						"currentState", group.Status.State,
						"targetState", "secondary")

					// Call the same method we use for safe mode
					if err := r.setVolumeReplicationState(ctx, volRep, group.Namespace, "secondary"); err != nil {
						logger.Error(err, "Failed to set volume replication to secondary",
							"component", comp.Name,
							"volumeReplication", volRep)
						// Continue despite errors to try to set all volume replications
					} else {
						logger.Info("Successfully requested secondary state for volume replication",
							"component", comp.Name,
							"volumeReplication", volRep)
					}
				}
			}
		}

		// Suspend Flux resources if defined
		if len(group.Spec.ParentFluxResources) > 0 {
			logger.Info("Suspending Flux resources", "resourceCount", len(group.Spec.ParentFluxResources))
			if err := r.suspendFluxResources(ctx, group.Spec.ParentFluxResources, group.Namespace); err != nil {
				logger.Error(err, "Failed to suspend Flux resources")
				// Continue with the operation even if we can't suspend Flux resources
				// This is deliberate to avoid blocking the failover
			}
		}

		// Deactivate virtual services
		for _, comp := range group.Spec.Components {
			if len(comp.VirtualServices) > 0 {
				logger.Info("Updating virtual services for component", "component", comp.Name)
				// In production code, this would update the virtual services to point away from this cluster
			}
		}

		// Verify all workloads are scaled down before finalizing
		allScaledDown := true
		for _, comp := range group.Spec.Components {
			for _, workload := range comp.Workloads {
				isDown, err := r.isWorkloadScaledDown(ctx, workload.Kind, workload.Name, group.Namespace)
				if err != nil {
					logger.Error(err, "Failed to check if workload is scaled down",
						"kind", workload.Kind, "name", workload.Name)
					return "FAILED", err
				}

				if !isDown {
					allScaledDown = false
					logger.Info("Waiting for workload to scale down completely",
						"kind", workload.Kind, "name", workload.Name)
					break
				}
			}
			if !allScaledDown {
				break
			}
		}

		if !allScaledDown {
			// Not all workloads are scaled down yet, return IN_PROGRESS
			logger.Info("Not all workloads are fully scaled down, returning IN_PROGRESS")
			return "IN_PROGRESS", nil
		}

		// Record the time of the failback completion
		group.Status.LastFailoverTime = time.Now().Format(time.RFC3339)

		// Update the state to STANDBY
		logger.Info("All workloads scaled down, setting state to STANDBY")
		group.Status.State = string(crdv1alpha1.FailoverGroupStateStandby)
	}

	// Update the group status
	if err := r.updateFailoverGroupWithRetry(ctx, group); err != nil {
		logger.Error(err, "Failed to update FailoverGroup status")
		return "FAILED", err
	}

	return "SUCCESS", nil
}

// triggerFluxReconciliation forces Flux to reconcile resources
func (r *FailoverReconciler) triggerFluxReconciliation(ctx context.Context, resources []crdv1alpha1.ResourceRef, namespace string) error {
	logger := r.Log.WithValues("action", "triggerFluxReconciliation", "namespace", namespace)

	for _, resource := range resources {
		logger.Info("Triggering reconciliation for Flux resource", "kind", resource.Kind, "name", resource.Name)

		// This is a stub implementation - in a real environment, you would use the Flux API
		// to trigger an immediate reconciliation
		switch resource.Kind {
		case "HelmRelease":
			if err := r.triggerHelmReleaseReconciliation(ctx, resource.Name, namespace); err != nil {
				return err
			}
		case "Kustomization":
			if err := r.triggerKustomizationReconciliation(ctx, resource.Name, namespace); err != nil {
				return err
			}
		default:
			logger.Info("Unsupported flux resource kind for manual reconciliation", "kind", resource.Kind)
		}
	}

	return nil
}

// triggerHelmReleaseReconciliation forces a HelmRelease to reconcile
func (r *FailoverReconciler) triggerHelmReleaseReconciliation(ctx context.Context, name, namespace string) error {
	logger := r.Log.WithValues("type", "HelmRelease", "name", name, "namespace", namespace)
	logger.Info("Triggering HelmRelease reconciliation")

	maxRetries := 5
	retryDelay := 200 * time.Millisecond

	for i := 0; i < maxRetries; i++ {
		// Create an unstructured object for the HelmRelease
		helmRelease := &unstructured.Unstructured{}
		helmRelease.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "helm.toolkit.fluxcd.io",
			Version: "v2beta1",
			Kind:    "HelmRelease",
		})

		// Get the HelmRelease
		if err := r.Get(ctx, types.NamespacedName{
			Name:      name,
			Namespace: namespace,
		}, helmRelease); err != nil {
			return client.IgnoreNotFound(err)
		}

		// Add reconciliation annotation to force reconciliation
		annotations := helmRelease.GetAnnotations()
		if annotations == nil {
			annotations = make(map[string]string)
		}

		// Set the requestedAt annotation with current timestamp
		// This is the standard way to force Flux to reconcile a resource
		annotations["reconcile.fluxcd.io/requestedAt"] = time.Now().Format(time.RFC3339Nano)

		// Also add our own annotation as a backup mechanism
		annotations["failover-operator.hahomelabs.com/reconcile"] = time.Now().Format(time.RFC3339)

		helmRelease.SetAnnotations(annotations)

		// Update the HelmRelease to apply the annotations
		err := r.Update(ctx, helmRelease)
		if err == nil {
			logger.Info("Successfully requested HelmRelease reconciliation")
			return nil
		}

		// If it's a conflict error, retry
		if errors.IsConflict(err) {
			logger.Info("Conflict updating HelmRelease for reconciliation, retrying",
				"attempt", i+1, "maxRetries", maxRetries)

			// Wait before retry with exponential backoff
			time.Sleep(retryDelay)
			retryDelay = retryDelay * 2
			continue
		}

		// Not a conflict error
		logger.Error(err, "Failed to update HelmRelease for forced reconciliation")
		return err
	}

	return fmt.Errorf("failed to update HelmRelease for reconciliation after %d retries", maxRetries)
}

// triggerKustomizationReconciliation forces a Kustomization to reconcile
func (r *FailoverReconciler) triggerKustomizationReconciliation(ctx context.Context, name, namespace string) error {
	logger := r.Log.WithValues("type", "Kustomization", "name", name, "namespace", namespace)
	logger.Info("Triggering Kustomization reconciliation")

	maxRetries := 5
	retryDelay := 200 * time.Millisecond

	for i := 0; i < maxRetries; i++ {
		// Create an unstructured object for the Kustomization
		kustomization := &unstructured.Unstructured{}
		kustomization.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "kustomize.toolkit.fluxcd.io",
			Version: "v1beta2", // Using v1beta2 as per the flux manager implementation
			Kind:    "Kustomization",
		})

		// Get the Kustomization
		if err := r.Get(ctx, types.NamespacedName{
			Name:      name,
			Namespace: namespace,
		}, kustomization); err != nil {
			return client.IgnoreNotFound(err)
		}

		// Add reconciliation annotation to force reconciliation
		annotations := kustomization.GetAnnotations()
		if annotations == nil {
			annotations = make(map[string]string)
		}

		// Set the requestedAt annotation with current timestamp
		// This is the standard way to force Flux to reconcile a resource
		annotations["reconcile.fluxcd.io/requestedAt"] = time.Now().Format(time.RFC3339Nano)

		// Also add our own annotation as a backup mechanism
		annotations["failover-operator.hahomelabs.com/reconcile"] = time.Now().Format(time.RFC3339)

		kustomization.SetAnnotations(annotations)

		// Update the Kustomization to apply the annotations
		err := r.Update(ctx, kustomization)
		if err == nil {
			logger.Info("Successfully requested Kustomization reconciliation")
			return nil
		}

		// If it's a conflict error, retry
		if errors.IsConflict(err) {
			logger.Info("Conflict updating Kustomization for reconciliation, retrying",
				"attempt", i+1, "maxRetries", maxRetries)

			// Wait before retry with exponential backoff
			time.Sleep(retryDelay)
			retryDelay = retryDelay * 2
			continue
		}

		// Not a conflict error
		logger.Error(err, "Failed to update Kustomization for forced reconciliation")
		return err
	}

	return fmt.Errorf("failed to update Kustomization for reconciliation after %d retries", maxRetries)
}

// handleDeletion handles cleanup when a Failover resource is being deleted
func (r *FailoverReconciler) handleDeletion(ctx context.Context, failover *crdv1alpha1.Failover) (ctrl.Result, error) {
	logger := r.Log.WithValues("failover", fmt.Sprintf("%s/%s", failover.Namespace, failover.Name))
	logger.Info("Handling Failover deletion")

	// Perform any necessary cleanup for the Failover resource

	// Remove finalizer to allow deletion to proceed
	controllerutil.RemoveFinalizer(failover, "failover.hahomelabs.com/finalizer")
	if err := r.Update(ctx, failover); err != nil {
		logger.Error(err, "Failed to remove finalizer")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// getFailoverStartTime extracts the start time from conditions
func getFailoverStartTime(conditions []metav1.Condition) *time.Time {
	for _, cond := range conditions {
		if cond.Type == "Started" && cond.Status == metav1.ConditionTrue {
			t, err := time.Parse(time.RFC3339, cond.LastTransitionTime.Format(time.RFC3339))
			if err != nil {
				return nil
			}
			return &t
		}
	}
	return nil
}

// getCurrentClusterName returns the name of the current cluster
// In a real implementation, this would get the actual cluster name from configuration
func getCurrentClusterName() string {
	// Look for cluster name in environment variable first
	if clusterName := os.Getenv("CLUSTER_NAME"); clusterName != "" {
		return clusterName
	}

	// Try to read from a config file if it exists
	configFile := "/etc/failover-operator/cluster-name"
	if data, err := os.ReadFile(configFile); err == nil && len(data) > 0 {
		return strings.TrimSpace(string(data))
	}

	// Look for KUBE_CONTEXT env var (useful for testing)
	if kubeContext := os.Getenv("KUBE_CONTEXT"); kubeContext != "" {
		return kubeContext
	}

	// Finally fall back to the default
	return "current-cluster"
}

// SetupWithManager sets up the controller with the Manager.
func (r *FailoverReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Get the cluster name from environment variable
	r.ClusterName = getCurrentClusterName()
	r.Log.Info("Starting Failover controller", "clusterName", r.ClusterName)

	return ctrl.NewControllerManagedBy(mgr).
		For(&crdv1alpha1.Failover{}).
		Complete(r)
}

// scaleDownDeployment scales down a Deployment
func (r *FailoverReconciler) scaleDownDeployment(ctx context.Context, name, namespace string) error {
	// Get the deployment
	deployment := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, deployment); err != nil {
		if errors.IsNotFound(err) {
			// If not found, log and continue (not an error)
			return nil
		}
		return err
	}

	// Set replicas to 0
	var zero int32 = 0
	deployment.Spec.Replicas = &zero

	// Update the deployment
	return r.Update(ctx, deployment)
}

// scaleDownStatefulSet scales down a StatefulSet
func (r *FailoverReconciler) scaleDownStatefulSet(ctx context.Context, name, namespace string) error {
	// Get the statefulset
	statefulset := &appsv1.StatefulSet{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, statefulset); err != nil {
		if errors.IsNotFound(err) {
			// If not found, log and continue (not an error)
			return nil
		}
		return err
	}

	// Set replicas to 0
	var zero int32 = 0
	statefulset.Spec.Replicas = &zero

	// Update the statefulset
	return r.Update(ctx, statefulset)
}

// suspendCronJob suspends a CronJob
func (r *FailoverReconciler) suspendCronJob(ctx context.Context, name, namespace string) error {
	// Get the cronjob
	cronjob := &batchv1.CronJob{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, cronjob); err != nil {
		if errors.IsNotFound(err) {
			// If not found, log and continue (not an error)
			return nil
		}
		return err
	}

	// Set suspended to true
	suspended := true
	cronjob.Spec.Suspend = &suspended

	// Update the cronjob
	return r.Update(ctx, cronjob)
}

// resumeFluxResources resumes the flux resources in the parent list
func (r *FailoverReconciler) resumeFluxResources(ctx context.Context, resources []crdv1alpha1.ResourceRef, namespace string) error {
	logger := r.Log.WithValues("action", "resumeFluxResources", "namespace", namespace)

	for _, resource := range resources {
		logger.Info("Resuming Flux resource", "kind", resource.Kind, "name", resource.Name)

		switch resource.Kind {
		case "HelmRelease":
			if err := r.resumeHelmRelease(ctx, resource.Name, namespace); err != nil {
				return err
			}
		case "Kustomization":
			if err := r.resumeKustomization(ctx, resource.Name, namespace); err != nil {
				return err
			}
		default:
			logger.Info("Unsupported flux resource kind, skipping", "kind", resource.Kind)
		}
	}

	return nil
}

// suspendFluxResources suspends the flux resources in the parent list
func (r *FailoverReconciler) suspendFluxResources(ctx context.Context, resources []crdv1alpha1.ResourceRef, namespace string) error {
	logger := r.Log.WithValues("action", "suspendFluxResources", "namespace", namespace)

	for _, resource := range resources {
		logger.Info("Suspending Flux resource", "kind", resource.Kind, "name", resource.Name)

		switch resource.Kind {
		case "HelmRelease":
			if err := r.suspendHelmRelease(ctx, resource.Name, namespace); err != nil {
				return err
			}
		case "Kustomization":
			if err := r.suspendKustomization(ctx, resource.Name, namespace); err != nil {
				return err
			}
		default:
			logger.Info("Unsupported flux resource kind, skipping", "kind", resource.Kind)
		}
	}

	return nil
}

// resumeHelmRelease resumes a HelmRelease resource
func (r *FailoverReconciler) resumeHelmRelease(ctx context.Context, name, namespace string) error {
	// This is a stub implementation - for a complete implementation, use the Flux API
	// to get and modify the HelmRelease resource
	logger := r.Log.WithValues("type", "HelmRelease", "name", name, "namespace", namespace)
	logger.Info("Would resume HelmRelease (stub implementation)")

	// In a real implementation:
	// 1. Get the HelmRelease as unstructured.Unstructured
	// 2. Set .spec.suspend = false
	// 3. Update the HelmRelease

	return nil
}

// suspendHelmRelease suspends a HelmRelease resource
func (r *FailoverReconciler) suspendHelmRelease(ctx context.Context, name, namespace string) error {
	// This is a stub implementation - for a complete implementation, use the Flux API
	// to get and modify the HelmRelease resource
	logger := r.Log.WithValues("type", "HelmRelease", "name", name, "namespace", namespace)
	logger.Info("Would suspend HelmRelease (stub implementation)")

	// In a real implementation:
	// 1. Get the HelmRelease as unstructured.Unstructured
	// 2. Set .spec.suspend = true
	// 3. Update the HelmRelease

	return nil
}

// resumeKustomization resumes a Kustomization resource
func (r *FailoverReconciler) resumeKustomization(ctx context.Context, name, namespace string) error {
	// This is a stub implementation - for a complete implementation, use the Flux API
	// to get and modify the Kustomization resource
	logger := r.Log.WithValues("type", "Kustomization", "name", name, "namespace", namespace)
	logger.Info("Would resume Kustomization (stub implementation)")

	// In a real implementation:
	// 1. Get the Kustomization as unstructured.Unstructured
	// 2. Set .spec.suspend = false
	// 3. Update the Kustomization

	return nil
}

// suspendKustomization suspends a Kustomization resource
func (r *FailoverReconciler) suspendKustomization(ctx context.Context, name, namespace string) error {
	// This is a stub implementation - for a complete implementation, use the Flux API
	// to get and modify the Kustomization resource
	logger := r.Log.WithValues("type", "Kustomization", "name", name, "namespace", namespace)
	logger.Info("Would suspend Kustomization (stub implementation)")

	// In a real implementation:
	// 1. Get the Kustomization as unstructured.Unstructured
	// 2. Set .spec.suspend = true
	// 3. Update the Kustomization

	return nil
}

// checkWorkloadReady checks if a workload is ready (scaled up and running)
func (r *FailoverReconciler) checkWorkloadReady(ctx context.Context, kind, name, namespace string) (bool, error) {
	logger := r.Log.WithValues("kind", kind, "name", name, "namespace", namespace)

	switch kind {
	case "Deployment":
		return r.isDeploymentReady(ctx, name, namespace)
	case "StatefulSet":
		return r.isStatefulSetReady(ctx, name, namespace)
	case "CronJob":
		// CronJobs are considered ready if they exist and are not suspended
		return r.isCronJobReady(ctx, name, namespace)
	default:
		logger.Info("Unsupported workload kind for readiness check, skipping", "kind", kind)
		return true, nil // Skip unknown workload kinds
	}
}

// isDeploymentReady checks if a Deployment is ready
func (r *FailoverReconciler) isDeploymentReady(ctx context.Context, name, namespace string) (bool, error) {
	logger := r.Log.WithValues("type", "Deployment", "name", name, "namespace", namespace)

	// Get the deployment
	deployment := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, deployment); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Deployment not found")
			return false, nil
		}
		return false, err
	}

	// Check if the deployment has at least one replica
	if deployment.Spec.Replicas == nil || *deployment.Spec.Replicas == 0 {
		logger.Info("Deployment has 0 replicas")
		return false, nil
	}

	// Check if all replicas are ready
	if deployment.Status.ReadyReplicas != *deployment.Spec.Replicas {
		logger.Info("Not all replicas are ready",
			"readyReplicas", deployment.Status.ReadyReplicas,
			"desiredReplicas", *deployment.Spec.Replicas)
		return false, nil
	}

	logger.Info("Deployment is ready")
	return true, nil
}

// isStatefulSetReady checks if a StatefulSet is ready
func (r *FailoverReconciler) isStatefulSetReady(ctx context.Context, name, namespace string) (bool, error) {
	logger := r.Log.WithValues("type", "StatefulSet", "name", name, "namespace", namespace)

	// Get the statefulset
	statefulset := &appsv1.StatefulSet{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, statefulset); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("StatefulSet not found")
			return false, nil
		}
		return false, err
	}

	// Check if the statefulset has at least one replica
	if statefulset.Spec.Replicas == nil || *statefulset.Spec.Replicas == 0 {
		logger.Info("StatefulSet has 0 replicas")
		return false, nil
	}

	// Check if all replicas are ready
	if statefulset.Status.ReadyReplicas != *statefulset.Spec.Replicas {
		logger.Info("Not all replicas are ready",
			"readyReplicas", statefulset.Status.ReadyReplicas,
			"desiredReplicas", *statefulset.Spec.Replicas)
		return false, nil
	}

	logger.Info("StatefulSet is ready")
	return true, nil
}

// isCronJobReady checks if a CronJob is ready (not suspended)
func (r *FailoverReconciler) isCronJobReady(ctx context.Context, name, namespace string) (bool, error) {
	logger := r.Log.WithValues("type", "CronJob", "name", name, "namespace", namespace)

	// Get the cronjob
	cronjob := &batchv1.CronJob{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, cronjob); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("CronJob not found")
			return false, nil
		}
		return false, err
	}

	// Check if the cronjob is suspended
	if cronjob.Spec.Suspend != nil && *cronjob.Spec.Suspend {
		logger.Info("CronJob is suspended")
		return false, nil
	}

	logger.Info("CronJob is ready")
	return true, nil
}

// isWorkloadScaledDown checks if a workload is scaled down to 0 replicas
func (r *FailoverReconciler) isWorkloadScaledDown(ctx context.Context, kind, name, namespace string) (bool, error) {
	logger := r.Log.WithValues("kind", kind, "name", name, "namespace", namespace)

	switch kind {
	case "Deployment":
		deployment := &appsv1.Deployment{}
		if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, deployment); err != nil {
			if errors.IsNotFound(err) {
				// If not found, consider it as scaled down
				return true, nil
			}
			return false, err
		}

		// Check if replicas are 0
		return deployment.Spec.Replicas == nil || *deployment.Spec.Replicas == 0, nil

	case "StatefulSet":
		statefulset := &appsv1.StatefulSet{}
		if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, statefulset); err != nil {
			if errors.IsNotFound(err) {
				// If not found, consider it as scaled down
				return true, nil
			}
			return false, err
		}

		// Check if replicas are 0
		return statefulset.Spec.Replicas == nil || *statefulset.Spec.Replicas == 0, nil

	case "CronJob":
		cronjob := &batchv1.CronJob{}
		if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, cronjob); err != nil {
			if errors.IsNotFound(err) {
				// If not found, consider it as scaled down
				return true, nil
			}
			return false, err
		}

		// Check if suspended
		return cronjob.Spec.Suspend != nil && *cronjob.Spec.Suspend, nil

	default:
		logger.Info("Unsupported workload kind for scale check, skipping", "kind", kind)
		return true, nil // Skip unknown workload kinds
	}
}

// scaleWorkload scales a workload to the specified replica count
func (r *FailoverReconciler) scaleWorkload(ctx context.Context, kind, name, namespace string, replicas int32) error {
	logger := r.Log.WithValues("kind", kind, "name", name, "namespace", namespace, "replicas", replicas)

	switch kind {
	case "Deployment":
		return r.scaleDeployment(ctx, name, namespace, replicas)
	case "StatefulSet":
		return r.scaleStatefulSet(ctx, name, namespace, replicas)
	case "CronJob":
		suspended := replicas == 0
		return r.scaleCronJob(ctx, name, namespace, suspended)
	default:
		logger.Info("Unsupported workload kind for scaling, skipping", "kind", kind)
		return nil // Skip unknown workload kinds
	}
}

// scaleDeployment scales a deployment to the specified replica count
func (r *FailoverReconciler) scaleDeployment(ctx context.Context, name, namespace string, replicas int32) error {
	logger := r.Log.WithValues("type", "Deployment", "name", name, "namespace", namespace, "replicas", replicas)

	// Get the deployment
	deployment := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, deployment); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Deployment not found")
			return nil
		}
		return err
	}

	// Set replicas
	deployment.Spec.Replicas = &replicas

	// Update the deployment
	logger.Info("Scaling deployment")
	return r.Update(ctx, deployment)
}

// scaleStatefulSet scales a statefulset to the specified replica count
func (r *FailoverReconciler) scaleStatefulSet(ctx context.Context, name, namespace string, replicas int32) error {
	logger := r.Log.WithValues("type", "StatefulSet", "name", name, "namespace", namespace, "replicas", replicas)

	// Get the statefulset
	statefulset := &appsv1.StatefulSet{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, statefulset); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("StatefulSet not found")
			return nil
		}
		return err
	}

	// Set replicas
	statefulset.Spec.Replicas = &replicas

	// Update the statefulset
	logger.Info("Scaling statefulset")
	return r.Update(ctx, statefulset)
}

// scaleCronJob sets the suspended state of a cronjob
func (r *FailoverReconciler) scaleCronJob(ctx context.Context, name, namespace string, suspended bool) error {
	logger := r.Log.WithValues("type", "CronJob", "name", name, "namespace", namespace, "suspended", suspended)

	// Get the cronjob
	cronjob := &batchv1.CronJob{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, cronjob); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("CronJob not found")
			return nil
		}
		return err
	}

	// Set suspended state
	cronjob.Spec.Suspend = &suspended

	// Update the cronjob
	logger.Info("Setting cronjob suspended state")
	return r.Update(ctx, cronjob)
}

// addFluxReconcileAnnotation adds the Flux reconcile=disabled annotation to a resource
func (r *FailoverReconciler) addFluxReconcileAnnotation(ctx context.Context, kind, name, namespace string) error {
	logger := r.Log.WithValues("kind", kind, "name", name, "namespace", namespace)
	logger.Info("Adding Flux reconcile=disabled annotation")

	switch kind {
	case "Deployment":
		return r.addFluxAnnotationToDeployment(ctx, name, namespace)
	case "StatefulSet":
		return r.addFluxAnnotationToStatefulSet(ctx, name, namespace)
	case "CronJob":
		return r.addFluxAnnotationToCronJob(ctx, name, namespace)
	default:
		logger.Info("Unsupported workload kind for flux annotation, skipping", "kind", kind)
		return nil
	}
}

// removeFluxReconcileAnnotation removes the Flux reconcile annotation from a resource
func (r *FailoverReconciler) removeFluxReconcileAnnotation(ctx context.Context, kind, name, namespace string) error {
	logger := r.Log.WithValues("kind", kind, "name", name, "namespace", namespace)
	logger.Info("Removing Flux reconcile annotation")

	switch kind {
	case "Deployment":
		return r.removeFluxAnnotationFromDeployment(ctx, name, namespace)
	case "StatefulSet":
		return r.removeFluxAnnotationFromStatefulSet(ctx, name, namespace)
	case "CronJob":
		return r.removeFluxAnnotationFromCronJob(ctx, name, namespace)
	default:
		logger.Info("Unsupported workload kind for flux annotation, skipping", "kind", kind)
		return nil
	}
}

// addFluxAnnotationToDeployment adds the Flux reconcile=disabled annotation to a Deployment
func (r *FailoverReconciler) addFluxAnnotationToDeployment(ctx context.Context, name, namespace string) error {
	// Get the deployment
	deployment := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, deployment); err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}

	// Add the annotation
	if deployment.Annotations == nil {
		deployment.Annotations = make(map[string]string)
	}
	deployment.Annotations[FluxReconcileAnnotation] = DisabledValue

	// Update the deployment
	return r.Update(ctx, deployment)
}

// removeFluxAnnotationFromDeployment removes the Flux reconcile annotation from a Deployment
func (r *FailoverReconciler) removeFluxAnnotationFromDeployment(ctx context.Context, name, namespace string) error {
	// Get the deployment
	deployment := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, deployment); err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}

	// Remove the annotation
	if deployment.Annotations != nil {
		delete(deployment.Annotations, FluxReconcileAnnotation)
	}

	// Update the deployment
	return r.Update(ctx, deployment)
}

// addFluxAnnotationToStatefulSet adds the Flux reconcile=disabled annotation to a StatefulSet
func (r *FailoverReconciler) addFluxAnnotationToStatefulSet(ctx context.Context, name, namespace string) error {
	// Get the statefulset
	statefulset := &appsv1.StatefulSet{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, statefulset); err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}

	// Add the annotation
	if statefulset.Annotations == nil {
		statefulset.Annotations = make(map[string]string)
	}
	statefulset.Annotations[FluxReconcileAnnotation] = DisabledValue

	// Update the statefulset
	return r.Update(ctx, statefulset)
}

// removeFluxAnnotationFromStatefulSet removes the Flux reconcile annotation from a StatefulSet
func (r *FailoverReconciler) removeFluxAnnotationFromStatefulSet(ctx context.Context, name, namespace string) error {
	// Get the statefulset
	statefulset := &appsv1.StatefulSet{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, statefulset); err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}

	// Remove the annotation
	if statefulset.Annotations != nil {
		delete(statefulset.Annotations, FluxReconcileAnnotation)
	}

	// Update the statefulset
	return r.Update(ctx, statefulset)
}

// addFluxAnnotationToCronJob adds the Flux reconcile=disabled annotation to a CronJob
func (r *FailoverReconciler) addFluxAnnotationToCronJob(ctx context.Context, name, namespace string) error {
	// Get the cronjob
	cronjob := &batchv1.CronJob{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, cronjob); err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}

	// Add the annotation
	if cronjob.Annotations == nil {
		cronjob.Annotations = make(map[string]string)
	}
	cronjob.Annotations[FluxReconcileAnnotation] = DisabledValue

	// Update the cronjob
	return r.Update(ctx, cronjob)
}

// removeFluxAnnotationFromCronJob removes the Flux reconcile annotation from a CronJob
func (r *FailoverReconciler) removeFluxAnnotationFromCronJob(ctx context.Context, name, namespace string) error {
	// Get the cronjob
	cronjob := &batchv1.CronJob{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, cronjob); err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}

	// Remove the annotation
	if cronjob.Annotations != nil {
		delete(cronjob.Annotations, FluxReconcileAnnotation)
	}

	// Update the cronjob
	return r.Update(ctx, cronjob)
}

// initiateEmergencyFailback initiates an emergency failback by creating a new failover in the reverse direction
// This is called when a failover operation fails to return the system to a stable state
func (r *FailoverReconciler) initiateEmergencyFailback(ctx context.Context, failover *crdv1alpha1.Failover) (ctrl.Result, error) {
	logger := r.Log.WithValues("failover", fmt.Sprintf("%s/%s", failover.Namespace, failover.Name))
	logger.Info("Initiating emergency failback", "originalTarget", failover.Spec.TargetCluster)

	// Create an emergency failover in the opposite direction
	emergencyFailover := &crdv1alpha1.Failover{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-emergency", failover.Name),
			Namespace: failover.Namespace,
			Annotations: map[string]string{
				"failover.hahomelabs.com/failback-for":   failover.Name,
				"failover.hahomelabs.com/failure-reason": getFailureReason(failover.Status.Conditions),
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: failover.APIVersion,
					Kind:       failover.Kind,
					Name:       failover.Name,
					UID:        failover.UID,
					Controller: boolPtr(true),
				},
			},
		},
		Spec: crdv1alpha1.FailoverSpec{
			// Target the source cluster (i.e., current cluster) to reverse the direction
			TargetCluster: r.ClusterName,
			// Mark as emergency operation
			Type: "emergency",
			// Use the same groups that were being failed over
			FailoverGroups: failover.Spec.FailoverGroups,
		},
	}

	// Create the emergency failover
	logger.Info("Creating emergency failback", "name", emergencyFailover.Name, "targetCluster", emergencyFailover.Spec.TargetCluster)
	if err := r.Create(ctx, emergencyFailover); err != nil {
		logger.Error(err, "Failed to create emergency failback")
		return ctrl.Result{}, err
	}

	// Update the original failover with a reference to the emergency failback
	condition := metav1.Condition{
		Type:               "EmergencyFailbackInitiated",
		Status:             metav1.ConditionTrue,
		LastTransitionTime: metav1.Now(),
		Reason:             "FailoverFailed",
		Message:            fmt.Sprintf("Emergency failback created: %s", emergencyFailover.Name),
	}
	meta.SetStatusCondition(&failover.Status.Conditions, condition)

	if err := r.Status().Update(ctx, failover); err != nil {
		logger.Error(err, "Failed to update original failover with emergency failback status")
		// Don't return error as the emergency failover was already created
	}

	return ctrl.Result{}, nil
}

// getFailureReason extracts the failure reason from conditions
func getFailureReason(conditions []metav1.Condition) string {
	for _, cond := range conditions {
		if cond.Type == "Failed" && cond.Status == metav1.ConditionTrue {
			return fmt.Sprintf("%s: %s", cond.Reason, cond.Message)
		}
	}
	return "Unknown failure"
}

// boolPtr returns a pointer to a bool
func boolPtr(b bool) *bool {
	return &b
}

// setVolumeReplicationState sets the state of a volume replication with retries
func (r *FailoverReconciler) setVolumeReplicationState(ctx context.Context, name, namespace, state string) error {
	logger := r.Log.WithValues("action", "setVolumeReplicationState", "name", name, "namespace", namespace, "state", state)
	logger.Info("Setting volume replication state")

	maxRetries := 5
	retryDelay := 200 * time.Millisecond

	for i := 0; i < maxRetries; i++ {
		// Use dynamic client to get the VolumeReplication resource
		u := &unstructured.Unstructured{}
		u.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "replication.storage.openshift.io",
			Version: "v1alpha1",
			Kind:    "VolumeReplication",
		})

		// Get the VolumeReplication resource
		err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, u)
		if err != nil {
			if errors.IsNotFound(err) {
				logger.Info("VolumeReplication not found, skipping", "name", name, "namespace", namespace)
				return nil
			}
			logger.Error(err, "Failed to get VolumeReplication", "name", name)
			return err
		}

		// Check current spec state
		spec, found, err := unstructured.NestedMap(u.Object, "spec")
		if err != nil || !found {
			logger.Error(err, "Failed to get spec from VolumeReplication", "found", found)
			return fmt.Errorf("could not get spec from VolumeReplication: %v", err)
		}

		currentState, found, err := unstructured.NestedString(spec, "replicationState")
		if err != nil {
			logger.Error(err, "Failed to get replicationState from spec")
			return err
		}

		// Only update if the state is different
		if !found || currentState != state {
			logger.Info("Updating VolumeReplication state",
				"currentState", currentState,
				"newState", state,
				"attempt", i+1)

			// Update the replicationState field
			err = unstructured.SetNestedField(u.Object, state, "spec", "replicationState")
			if err != nil {
				logger.Error(err, "Failed to set replicationState field")
				return err
			}

			// Update the resource
			err := r.Update(ctx, u)
			if err == nil {
				logger.Info("Successfully updated VolumeReplication state")
				return nil
			}

			// Handle conflict error with retry
			if errors.IsConflict(err) {
				logger.Info("Conflict updating VolumeReplication, retrying",
					"attempt", i+1, "maxRetries", maxRetries)

				// Wait before retry with exponential backoff
				time.Sleep(retryDelay)
				retryDelay = retryDelay * 2
				continue
			}

			// Not a conflict error
			logger.Error(err, "Failed to update VolumeReplication")
			return err
		} else {
			logger.Info("VolumeReplication already in desired state, skipping update",
				"state", state)
			return nil
		}
	}

	return fmt.Errorf("failed to update VolumeReplication state after %d retries", maxRetries)
}

// isAnyFailoverInProgress checks if any other Failover resources are currently in progress
// for the same FailoverGroups as the current failover
func (r *FailoverReconciler) isAnyFailoverInProgress(ctx context.Context, currentFailover *crdv1alpha1.Failover) (bool, error) {
	logger := r.Log.WithValues("currentFailover", fmt.Sprintf("%s/%s", currentFailover.Namespace, currentFailover.Name))

	// Create a map of the FailoverGroups in the current request for quick lookup
	currentGroups := make(map[string]bool)
	for _, group := range currentFailover.Spec.FailoverGroups {
		// Create a unique key for the group using namespace and name
		var namespace string
		if group.Namespace == "" {
			namespace = currentFailover.Namespace // Use failover namespace if group namespace is not specified
		} else {
			namespace = group.Namespace
		}
		groupKey := fmt.Sprintf("%s/%s", namespace, group.Name)
		currentGroups[groupKey] = true
	}

	// List all Failover resources
	failoverList := &crdv1alpha1.FailoverList{}
	if err := r.List(ctx, failoverList); err != nil {
		logger.Error(err, "Failed to list Failover resources")
		return false, err
	}

	// Check each Failover resource
	for _, failover := range failoverList.Items {
		// Skip the current failover
		if failover.Namespace == currentFailover.Namespace && failover.Name == currentFailover.Name {
			continue
		}

		// Check if this failover is in progress
		if failover.Status.Status == "IN_PROGRESS" {
			// Check if it affects any of the same FailoverGroups
			for _, group := range failover.Spec.FailoverGroups {
				var namespace string
				if group.Namespace == "" {
					namespace = failover.Namespace
				} else {
					namespace = group.Namespace
				}
				groupKey := fmt.Sprintf("%s/%s", namespace, group.Name)

				if currentGroups[groupKey] {
					// Found an in-progress failover that affects one of our groups
					logger.Info("Found another Failover in progress for group",
						"failoverName", failover.Name,
						"failoverNamespace", failover.Namespace,
						"groupName", group.Name,
						"groupNamespace", namespace,
						"startTime", getFailoverStartTime(failover.Status.Conditions))
					return true, nil
				}
			}
		}
	}

	// No other in-progress failovers found for the same groups
	return false, nil
}

// processWaitingFailovers finds the oldest waiting failover for each FailoverGroup and removes its waiting condition
// so it can proceed with the failover process on the next reconciliation
func (r *FailoverReconciler) processWaitingFailovers(ctx context.Context) error {
	logger := r.Log.WithValues("action", "processWaitingFailovers")

	// List all Failover resources
	failoverList := &crdv1alpha1.FailoverList{}
	if err := r.List(ctx, failoverList); err != nil {
		logger.Error(err, "Failed to list Failover resources")
		return err
	}

	// Map of active groups (those that have an IN_PROGRESS failover)
	// Key format: "namespace/name"
	activeGroups := make(map[string]bool)

	// First, identify all groups that have an active failover
	for _, failover := range failoverList.Items {
		if failover.Status.Status == "IN_PROGRESS" {
			// Add all groups from this failover to the activeGroups map
			for _, group := range failover.Spec.FailoverGroups {
				var namespace string
				if group.Namespace == "" {
					namespace = failover.Namespace
				} else {
					namespace = group.Namespace
				}
				groupKey := fmt.Sprintf("%s/%s", namespace, group.Name)
				activeGroups[groupKey] = true
			}
		}
	}

	// Map of groups to their oldest waiting failover
	// Key format: "namespace/name"
	type waitingFailover struct {
		failover *crdv1alpha1.Failover
		waitTime time.Time
	}
	oldestWaitingPerGroup := make(map[string]waitingFailover)

	// Find the oldest waiting failover for each group
	for i := range failoverList.Items {
		failover := &failoverList.Items[i]

		// Skip failovers that are already processing or completed
		if failover.Status.Status != "" {
			continue
		}

		// Check if this failover has a Waiting condition
		isWaiting := false
		waitingTime := time.Time{}

		for _, cond := range failover.Status.Conditions {
			if cond.Type == "Waiting" && cond.Status == metav1.ConditionTrue {
				isWaiting = true

				// Get the transition time
				t, err := time.Parse(time.RFC3339, cond.LastTransitionTime.Format(time.RFC3339))
				if err == nil {
					waitingTime = t
				}
				break
			}
		}

		if isWaiting {
			// Process each group in this waiting failover
			for _, group := range failover.Spec.FailoverGroups {
				var namespace string
				if group.Namespace == "" {
					namespace = failover.Namespace
				} else {
					namespace = group.Namespace
				}
				groupKey := fmt.Sprintf("%s/%s", namespace, group.Name)

				// Check if there's already an older waiting failover for this group
				current, exists := oldestWaitingPerGroup[groupKey]
				if !exists || waitingTime.Before(current.waitTime) {
					oldestWaitingPerGroup[groupKey] = waitingFailover{
						failover: failover,
						waitTime: waitingTime,
					}
				}
			}
		}
	}

	// Track which failovers we've processed to avoid processing the same one multiple times
	processedFailovers := make(map[string]bool)

	// Process the oldest waiting failover for each inactive group
	for groupKey, waiting := range oldestWaitingPerGroup {
		// Skip if this group is active
		if activeGroups[groupKey] {
			logger.Info("Group has an active failover, skipping waiting failovers", "group", groupKey)
			continue
		}

		failover := waiting.failover
		failoverKey := fmt.Sprintf("%s/%s", failover.Namespace, failover.Name)

		// Skip if we've already processed this failover
		if processedFailovers[failoverKey] {
			continue
		}

		// Check if all groups in this failover are inactive
		allGroupsInactive := true
		for _, group := range failover.Spec.FailoverGroups {
			var namespace string
			if group.Namespace == "" {
				namespace = failover.Namespace
			} else {
				namespace = group.Namespace
			}
			groupKey := fmt.Sprintf("%s/%s", namespace, group.Name)

			if activeGroups[groupKey] {
				allGroupsInactive = false
				break
			}
		}

		if !allGroupsInactive {
			logger.Info("Failover has some groups that are still active, waiting for all to be inactive",
				"name", failover.Name, "namespace", failover.Namespace)
			continue
		}

		// Mark this failover as processed
		processedFailovers[failoverKey] = true

		logger.Info("Processing waiting failover for inactive groups",
			"name", failover.Name,
			"namespace", failover.Namespace,
			"waitingSince", waiting.waitTime)

		// Find and remove the Waiting condition
		updated := false
		for i, cond := range failover.Status.Conditions {
			if cond.Type == "Waiting" && cond.Status == metav1.ConditionTrue {
				// Remove this condition (replace with the last one and shrink slice)
				lastIdx := len(failover.Status.Conditions) - 1
				failover.Status.Conditions[i] = failover.Status.Conditions[lastIdx]
				failover.Status.Conditions = failover.Status.Conditions[:lastIdx]
				updated = true
				break
			}
		}

		// Add a new condition indicating this failover is now allowed to proceed
		if updated {
			proceedCondition := metav1.Condition{
				Type:               "ReadyToProcess",
				Status:             metav1.ConditionTrue,
				LastTransitionTime: metav1.Now(),
				Reason:             "GroupsAvailable",
				Message:            "Target FailoverGroups are not being processed by other failovers, proceeding with this failover",
			}
			meta.SetStatusCondition(&failover.Status.Conditions, proceedCondition)

			// Update the failover
			if err := r.Status().Update(ctx, failover); err != nil {
				logger.Error(err, "Failed to update waiting failover status")
				return err
			}

			logger.Info("Successfully updated waiting failover to proceed")
		}
	}

	return nil
}
