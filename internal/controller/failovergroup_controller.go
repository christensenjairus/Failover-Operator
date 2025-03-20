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
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	crdv1alpha1 "github.com/christensenjairus/Failover-Operator/api/v1alpha1"
	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	types "k8s.io/apimachinery/pkg/types"
)

// FailoverGroupReconciler reconciles a FailoverGroup object
type FailoverGroupReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Log    logr.Logger
}

//+kubebuilder:rbac:groups=crd.hahomelabs.com,resources=failovergroups,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=crd.hahomelabs.com,resources=failovergroups/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=crd.hahomelabs.com,resources=failovergroups/finalizers,verbs=update
//+kubebuilder:rbac:groups=apps,resources=deployments;statefulsets,verbs=get;list;watch
//+kubebuilder:rbac:groups=batch,resources=cronjobs,verbs=get;list;watch
//+kubebuilder:rbac:groups=networking.istio.io,resources=virtualservices,verbs=get;list;watch
//+kubebuilder:rbac:groups=helm.toolkit.fluxcd.io,resources=helmreleases,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=kustomize.toolkit.fluxcd.io,resources=kustomizations,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=replication.storage.openshift.io,resources=volumereplications,verbs=get;list;watch;update;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *FailoverGroupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("failovergroup", req.NamespacedName)

	// Fetch the FailoverGroup instance
	failoverGroup := &crdv1alpha1.FailoverGroup{}
	if err := r.Get(ctx, req.NamespacedName, failoverGroup); err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Don't requeue
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		logger.Error(err, "Failed to get FailoverGroup")
		return ctrl.Result{}, err
	}

	// Add finalizer if it doesn't exist
	if !controllerutil.ContainsFinalizer(failoverGroup, "failovergroup.hahomelabs.com/finalizer") {
		logger.Info("Adding finalizer to FailoverGroup")
		controllerutil.AddFinalizer(failoverGroup, "failovergroup.hahomelabs.com/finalizer")
		if err := r.Update(ctx, failoverGroup); err != nil {
			logger.Error(err, "Failed to add finalizer to FailoverGroup")
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Handle deletion if marked for deletion
	if !failoverGroup.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, failoverGroup)
	}

	// Initialize status if empty
	if failoverGroup.Status.State == "" {
		logger.Info("Initializing FailoverGroup status")

		// Default to PRIMARY to allow Flux to operate normally from the start
		failoverGroup.Status.State = string(crdv1alpha1.FailoverGroupStatePrimary)
		failoverGroup.Status.Health = "OK" // Start with OK health by default

		// Initialize component statuses
		failoverGroup.Status.Components = make([]crdv1alpha1.ComponentStatus, 0, len(failoverGroup.Spec.Components))
		for _, comp := range failoverGroup.Spec.Components {
			compStatus := crdv1alpha1.ComponentStatus{
				Name:   comp.Name,
				Health: "OK", // Start with OK health by default
			}
			failoverGroup.Status.Components = append(failoverGroup.Status.Components, compStatus)
		}

		if err := r.updateStatusWithRetry(ctx, failoverGroup); err != nil {
			logger.Error(err, "Failed to initialize failoverGroup status")
			return ctrl.Result{}, err
		}

		// Return and requeue to continue with the reconciliation after status is initialized
		return ctrl.Result{Requeue: true}, nil
	}

	// Process components to update health status
	updatedHealth := r.calculateComponentHealth(ctx, failoverGroup)
	if updatedHealth != failoverGroup.Status.Health {
		logger.Info("Updating FailoverGroup health status",
			"previous", failoverGroup.Status.Health, "new", updatedHealth)
		failoverGroup.Status.Health = updatedHealth
		if err := r.updateStatusWithRetry(ctx, failoverGroup); err != nil {
			logger.Error(err, "Failed to update FailoverGroup health status")
			return ctrl.Result{}, err
		}
	}

	// Log appropriate messages based on the current state
	switch failoverGroup.Status.State {
	case string(crdv1alpha1.FailoverGroupStatePrimary):
		// If this is a PRIMARY group, verify that workloads are actually running
		verifyPrimaryState(ctx, logger, r, failoverGroup)
	case string(crdv1alpha1.FailoverGroupStateStandby):
		// For STANDBY groups, we just monitor health
		logger.V(1).Info("FailoverGroup is in STANDBY state")
	case string(crdv1alpha1.FailoverGroupStateFailback):
		// Group is transitioning from STANDBY to PRIMARY
		logger.Info("FailoverGroup is in FAILBACK state - transitioning from STANDBY to PRIMARY")
		// Reconcile more frequently during state transitions
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	case string(crdv1alpha1.FailoverGroupStateFailover):
		// Group is transitioning from PRIMARY to STANDBY
		logger.Info("FailoverGroup is in FAILOVER state - transitioning from PRIMARY to STANDBY")
		// Reconcile more frequently during state transitions
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	default:
		// Unknown state
		logger.Info("FailoverGroup is in unknown state", "state", failoverGroup.Status.State)
	}

	// Requeue periodically to ensure health stays current
	// More frequent reconciliation for groups in transitory states
	if failoverGroup.Status.State == string(crdv1alpha1.FailoverGroupStateFailback) ||
		failoverGroup.Status.State == string(crdv1alpha1.FailoverGroupStateFailover) {
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	// Update health check frequency to every 5 seconds as requested
	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}

// verifyPrimaryState checks if a PRIMARY FailoverGroup's workloads are actually running
func verifyPrimaryState(ctx context.Context, logger logr.Logger, r *FailoverGroupReconciler, failoverGroup *crdv1alpha1.FailoverGroup) {
	allReady := true
	for _, component := range failoverGroup.Spec.Components {
		for _, workload := range component.Workloads {
			ready, err := r.isWorkloadReady(ctx, workload.Kind, workload.Name, failoverGroup.Namespace)
			if err != nil {
				logger.Error(err, "Failed to check workload readiness",
					"kind", workload.Kind, "name", workload.Name)
				continue
			}

			if !ready {
				logger.Info("Workload not ready but FailoverGroup is PRIMARY",
					"kind", workload.Kind, "name", workload.Name)
				allReady = false
			}
		}
	}

	// If not all workloads are ready but we're in PRIMARY state, we'll log this inconsistency
	// but won't change the state automatically - this would typically be addressed by a Failover operation
	if !allReady {
		logger.Info("Warning: FailoverGroup is in PRIMARY state but some workloads are not ready")
	}
}

// calculateComponentHealth determines the overall health of the FailoverGroup based on component status
func (r *FailoverGroupReconciler) calculateComponentHealth(ctx context.Context, fg *crdv1alpha1.FailoverGroup) string {
	// Use the logger in debug statements if needed
	r.Log.V(1).Info("Calculating component health", "failovergroup", fmt.Sprintf("%s/%s", fg.Namespace, fg.Name))

	hasError := false
	hasDegraded := false

	// During transitory states (FAILOVER/FAILBACK), mark health as DEGRADED unless actual errors are found
	if fg.Status.State == string(crdv1alpha1.FailoverGroupStateFailback) ||
		fg.Status.State == string(crdv1alpha1.FailoverGroupStateFailover) {
		hasDegraded = true
		r.Log.Info("Group is in transitory state, marking health as at least DEGRADED",
			"state", fg.Status.State)
	}

	// Update component status and track overall health
	for i, comp := range fg.Spec.Components {
		health := "OK"
		message := ""

		// Check workloads if in PRIMARY state or transitioning to PRIMARY (FAILOVER)
		if fg.Status.State == string(crdv1alpha1.FailoverGroupStatePrimary) ||
			fg.Status.State == string(crdv1alpha1.FailoverGroupStateFailback) {
			workloadOk, workloadMsg := r.checkWorkloadsHealth(ctx, comp.Workloads, fg.Namespace)
			if !workloadOk {
				health = "DEGRADED"
				message = workloadMsg
				hasDegraded = true
			}
		}

		// Check volume replications
		if len(comp.VolumeReplications) > 0 {
			// This is a placeholder for volume replication health checks
			// In a real implementation, you would check the status of volume replications
		}

		// Check virtual services
		if len(comp.VirtualServices) > 0 {
			// This is a placeholder for virtual service health checks
			// In a real implementation, you would check the status of virtual services
		}

		// Update component status if changed
		if i < len(fg.Status.Components) {
			if fg.Status.Components[i].Health != health || fg.Status.Components[i].Message != message {
				fg.Status.Components[i].Health = health
				fg.Status.Components[i].Message = message
			}
		} else {
			// Add new component status
			fg.Status.Components = append(fg.Status.Components, crdv1alpha1.ComponentStatus{
				Name:    comp.Name,
				Health:  health,
				Message: message,
			})
		}

		if health == "ERROR" {
			hasError = true
		} else if health == "DEGRADED" {
			hasDegraded = true
		}
	}

	// Determine overall health
	if hasError {
		return "ERROR"
	} else if hasDegraded {
		return "DEGRADED"
	}

	return "OK"
}

// checkWorkloadsHealth checks the health of all workloads in a component
func (r *FailoverGroupReconciler) checkWorkloadsHealth(ctx context.Context, workloads []crdv1alpha1.ResourceRef, namespace string) (bool, string) {
	if len(workloads) == 0 {
		return true, ""
	}

	issues := []string{}
	allOk := true

	for _, workload := range workloads {
		ready, err := r.isWorkloadReady(ctx, workload.Kind, workload.Name, namespace)
		if err != nil {
			issues = append(issues, fmt.Sprintf("%s/%s: %v", workload.Kind, workload.Name, err))
			allOk = false
			continue
		}

		if !ready {
			issues = append(issues, fmt.Sprintf("%s/%s: not ready", workload.Kind, workload.Name))
			allOk = false
		}
	}

	message := strings.Join(issues, "; ")
	return allOk, message
}

// isWorkloadReady checks if a workload is ready
func (r *FailoverGroupReconciler) isWorkloadReady(ctx context.Context, kind, name, namespace string) (bool, error) {
	logger := r.Log.WithValues("kind", kind, "name", name, "namespace", namespace)

	switch kind {
	case "Deployment":
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

		return true, nil

	case "StatefulSet":
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

		return true, nil

	case "CronJob":
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

		return true, nil

	default:
		logger.Info("Unsupported workload kind for readiness check, skipping", "kind", kind)
		return true, nil // Skip unknown workload kinds
	}
}

// updateStatusWithRetry updates a FailoverGroup's status with retries to handle conflicts
func (r *FailoverGroupReconciler) updateStatusWithRetry(ctx context.Context, failoverGroup *crdv1alpha1.FailoverGroup) error {
	logger := r.Log.WithValues("failovergroup", fmt.Sprintf("%s/%s", failoverGroup.Namespace, failoverGroup.Name))
	maxRetries := 5
	retryDelay := 200 * time.Millisecond

	for i := 0; i < maxRetries; i++ {
		err := r.Status().Update(ctx, failoverGroup)
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
				Namespace: failoverGroup.Namespace,
				Name:      failoverGroup.Name,
			}, latestGroup); getErr != nil {
				logger.Error(getErr, "Failed to get latest FailoverGroup version")
				return getErr
			}

			// Preserve the status fields we wanted to update
			latestGroup.Status.State = failoverGroup.Status.State
			latestGroup.Status.Health = failoverGroup.Status.Health
			latestGroup.Status.Components = failoverGroup.Status.Components
			latestGroup.Status.LastFailoverTime = failoverGroup.Status.LastFailoverTime

			// Use the latest version for the next attempt
			failoverGroup = latestGroup

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

// handleDeletion handles cleanup when a FailoverGroup resource is being deleted
func (r *FailoverGroupReconciler) handleDeletion(ctx context.Context, failoverGroup *crdv1alpha1.FailoverGroup) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("failovergroup", fmt.Sprintf("%s/%s", failoverGroup.Namespace, failoverGroup.Name))
	logger.Info("Handling FailoverGroup deletion")

	// Check if there are any Failovers referencing this FailoverGroup
	hasActiveFailovers, err := r.checkForActiveFailovers(ctx, failoverGroup)
	if err != nil {
		logger.Error(err, "Failed to check for active Failovers")
		return ctrl.Result{}, err
	}

	if hasActiveFailovers {
		logger.Info("Cannot delete FailoverGroup because there are active Failovers referencing it")
		// Requeue to check again later
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	// If the group is already in PRIMARY state, we can just remove the finalizer
	if failoverGroup.Status.State == string(crdv1alpha1.FailoverGroupStatePrimary) {
		logger.Info("FailoverGroup is already in PRIMARY state, just removing finalizer")

		// Ensure Flux resources are unfrozen
		if len(failoverGroup.Spec.ParentFluxResources) > 0 {
			logger.Info("Ensuring Flux resources are resumed before deletion",
				"resourceCount", len(failoverGroup.Spec.ParentFluxResources))
			if err := r.resumeFluxResources(ctx, failoverGroup.Spec.ParentFluxResources, failoverGroup.Namespace); err != nil {
				logger.Error(err, "Failed to resume Flux resources during deletion")
				// Continue with deletion even if Flux resources can't be resumed
				// This is to avoid blocking deletion
			}
		}

		// Force reconcile the Flux resources one last time
		if len(failoverGroup.Spec.ParentFluxResources) > 0 {
			logger.Info("Triggering final reconciliation of Flux resources",
				"resourceCount", len(failoverGroup.Spec.ParentFluxResources))
			if err := r.triggerFluxReconciliation(ctx, failoverGroup); err != nil {
				logger.Error(err, "Failed to trigger Flux reconciliation during deletion")
				// Continue with deletion even if forcing reconciliation fails
			}
		}

		// Remove finalizer to allow deletion to proceed
		controllerutil.RemoveFinalizer(failoverGroup, "failovergroup.hahomelabs.com/finalizer")
		if err := r.Update(ctx, failoverGroup); err != nil {
			logger.Error(err, "Failed to remove finalizer")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// If we're in STANDBY state, we need to failback to PRIMARY before deletion
	if failoverGroup.Status.State == string(crdv1alpha1.FailoverGroupStateStandby) {
		logger.Info("FailoverGroup is in STANDBY state, triggering failback to PRIMARY before deletion")

		// First, handle volume replications to promote them
		if err := r.processVolumeReplications(ctx, failoverGroup, true); err != nil {
			logger.Error(err, "Failed to process volume replications during deletion")
			// Note the error but continue with the operation
		}

		// Resume Flux resources if they were suspended
		if len(failoverGroup.Spec.ParentFluxResources) > 0 {
			logger.Info("Resuming Flux resources during deletion",
				"resourceCount", len(failoverGroup.Spec.ParentFluxResources))
			if err := r.resumeFluxResources(ctx, failoverGroup.Spec.ParentFluxResources, failoverGroup.Namespace); err != nil {
				logger.Error(err, "Failed to resume Flux resources during deletion")
				// Continue with deletion even if Flux resources can't be resumed
			}
		}

		// Set state to transitory FAILOVER state (from STANDBY to PRIMARY)
		failoverGroup.Status.State = string(crdv1alpha1.FailoverGroupStateFailover)
		if err := r.updateStatusWithRetry(ctx, failoverGroup); err != nil {
			logger.Error(err, "Failed to update FailoverGroup state during deletion")
			return ctrl.Result{}, err
		}

		// Requeue to continue the process
		return ctrl.Result{Requeue: true}, nil
	}

	// If we're in FAILOVER state (transitioning from STANDBY to PRIMARY)
	if failoverGroup.Status.State == string(crdv1alpha1.FailoverGroupStateFailover) {
		// Check if workloads are now scaled up
		allScaledUp := true
		for _, comp := range failoverGroup.Spec.Components {
			for _, workload := range comp.Workloads {
				isUp, err := r.isWorkloadScaledUp(ctx, workload.Kind, workload.Name, failoverGroup.Namespace)
				if err != nil {
					logger.Error(err, "Failed to check if workload is scaled up during deletion",
						"kind", workload.Kind, "name", workload.Name)
					// Continue checking other workloads
					continue
				}

				if !isUp {
					allScaledUp = false
					logger.Info("Waiting for workload to scale up during deletion",
						"kind", workload.Kind, "name", workload.Name)
				}
			}
		}

		if !allScaledUp {
			// Requeue to check again later
			logger.Info("Not all workloads are fully scaled up, requeuing")
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}

		// Update to PRIMARY state
		logger.Info("All workloads scaled up, setting state to PRIMARY")
		failoverGroup.Status.State = string(crdv1alpha1.FailoverGroupStatePrimary)

		// Record the time of the failback completion
		failoverGroup.Status.LastFailoverTime = time.Now().Format(time.RFC3339)

		if err := r.updateStatusWithRetry(ctx, failoverGroup); err != nil {
			logger.Error(err, "Failed to update state to PRIMARY during deletion")
			return ctrl.Result{}, err
		}

		// Force reconcile the Flux resources one last time
		if len(failoverGroup.Spec.ParentFluxResources) > 0 {
			logger.Info("Triggering final reconciliation of Flux resources",
				"resourceCount", len(failoverGroup.Spec.ParentFluxResources))
			if err := r.triggerFluxReconciliation(ctx, failoverGroup); err != nil {
				logger.Error(err, "Failed to trigger Flux reconciliation during deletion")
				// Continue with deletion even if forcing reconciliation fails
			}
		}

		// Remove finalizer to allow deletion to proceed
		controllerutil.RemoveFinalizer(failoverGroup, "failovergroup.hahomelabs.com/finalizer")
		if err := r.Update(ctx, failoverGroup); err != nil {
			logger.Error(err, "Failed to remove finalizer")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// If we're in a transitory state (FAILBACK), just wait for it to complete naturally
	if failoverGroup.Status.State == string(crdv1alpha1.FailoverGroupStateFailback) {
		logger.Info("FailoverGroup is in FAILBACK state, waiting for completion before deletion")
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	// For any other state, set to PRIMARY and trigger a normal deletion sequence
	logger.Info("Setting FailoverGroup to PRIMARY state for deletion",
		"currentState", failoverGroup.Status.State)
	failoverGroup.Status.State = string(crdv1alpha1.FailoverGroupStatePrimary)
	if err := r.updateStatusWithRetry(ctx, failoverGroup); err != nil {
		logger.Error(err, "Failed to update state to PRIMARY during deletion")
		return ctrl.Result{}, err
	}

	// Requeue to continue the deletion process
	return ctrl.Result{Requeue: true}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *FailoverGroupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&crdv1alpha1.FailoverGroup{}).
		WithEventFilter(predicate.GenerationChangedPredicate{}). // Only reconcile on spec changes
		Complete(r)
}

// isWorkloadScaledUp checks if a workload is scaled to > 0 replicas
func (r *FailoverGroupReconciler) isWorkloadScaledUp(ctx context.Context, kind, name, namespace string) (bool, error) {
	logger := log.FromContext(ctx).WithValues("kind", kind, "name", name, "namespace", namespace)

	switch kind {
	case "Deployment":
		deployment := &appsv1.Deployment{}
		if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, deployment); err != nil {
			if errors.IsNotFound(err) {
				// If not found, consider it as not scaled up
				return false, nil
			}
			return false, err
		}

		// Check if replicas are > 0
		return deployment.Spec.Replicas != nil && *deployment.Spec.Replicas > 0, nil

	case "StatefulSet":
		statefulset := &appsv1.StatefulSet{}
		if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, statefulset); err != nil {
			if errors.IsNotFound(err) {
				// If not found, consider it as not scaled up
				return false, nil
			}
			return false, err
		}

		// Check if replicas are > 0
		return statefulset.Spec.Replicas != nil && *statefulset.Spec.Replicas > 0, nil

	case "CronJob":
		cronjob := &batchv1.CronJob{}
		if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, cronjob); err != nil {
			if errors.IsNotFound(err) {
				// If not found, consider it as not scaled up
				return false, nil
			}
			return false, err
		}

		// Check if not suspended
		return cronjob.Spec.Suspend == nil || !*cronjob.Spec.Suspend, nil

	default:
		logger.Info("Unsupported workload kind for scale check, skipping", "kind", kind)
		return true, nil // Skip unknown workload kinds
	}
}

// processVolumeReplications processes volume replications for a FailoverGroup
func (r *FailoverGroupReconciler) processVolumeReplications(ctx context.Context, failoverGroup *crdv1alpha1.FailoverGroup, makeActive bool) error {
	logger := log.FromContext(ctx).WithValues("failovergroup", fmt.Sprintf("%s/%s", failoverGroup.Namespace, failoverGroup.Name))

	// The target state for volume replications
	targetState := "secondary"
	if makeActive {
		targetState = "primary"
	}

	// Process volume replications for each component
	for _, comp := range failoverGroup.Spec.Components {
		if len(comp.VolumeReplications) > 0 {
			logger.Info("Processing volume replications",
				"component", comp.Name,
				"count", len(comp.VolumeReplications),
				"targetState", targetState)

			for _, volRep := range comp.VolumeReplications {
				if err := r.setVolumeReplicationState(ctx, volRep, failoverGroup.Namespace, targetState); err != nil {
					logger.Error(err, "Failed to set volume replication state",
						"component", comp.Name,
						"volumeReplication", volRep,
						"targetState", targetState)
					return err
				}
				logger.Info("Volume replication state update requested",
					"component", comp.Name,
					"volumeReplication", volRep,
					"targetState", targetState)
			}
		}
	}

	return nil
}

// setVolumeReplicationState updates the state of a VolumeReplication resource
func (r *FailoverGroupReconciler) setVolumeReplicationState(ctx context.Context, name, namespace, state string) error {
	logger := log.FromContext(ctx).WithValues("volumereplication", name, "namespace", namespace, "state", state)

	return r.retryOnConflict(ctx, func() error {
		// This is a stub implementation
		// In a real implementation, you would use the VolumeReplication CRD API to update the state
		logger.Info("Would update VolumeReplication state (stub implementation)")

		// In a real implementation:
		// 1. Get the VolumeReplication CR
		// 2. Update the .spec.replicationState field to the desired state
		// 3. Update the CR

		return nil
	})
}

// triggerFluxReconciliation forces Flux to reconcile resources for a FailoverGroup
func (r *FailoverGroupReconciler) triggerFluxReconciliation(ctx context.Context, failoverGroup *crdv1alpha1.FailoverGroup) error {
	logger := log.FromContext(ctx).WithValues("failovergroup", fmt.Sprintf("%s/%s", failoverGroup.Namespace, failoverGroup.Name))

	if len(failoverGroup.Spec.ParentFluxResources) == 0 {
		logger.Info("No Flux resources to reconcile")
		return nil
	}

	logger.Info("Triggering reconciliation for Flux resources",
		"count", len(failoverGroup.Spec.ParentFluxResources))

	for _, resource := range failoverGroup.Spec.ParentFluxResources {
		logger.Info("Triggering reconciliation for Flux resource",
			"kind", resource.Kind, "name", resource.Name)

		switch resource.Kind {
		case "HelmRelease":
			if err := r.triggerHelmReleaseReconciliation(ctx, resource.Name, failoverGroup.Namespace); err != nil {
				logger.Error(err, "Failed to trigger HelmRelease reconciliation")
				return err
			}
		case "Kustomization":
			if err := r.triggerKustomizationReconciliation(ctx, resource.Name, failoverGroup.Namespace); err != nil {
				logger.Error(err, "Failed to trigger Kustomization reconciliation")
				return err
			}
		default:
			logger.Info("Unsupported Flux resource kind for reconciliation", "kind", resource.Kind)
		}
	}

	return nil
}

// triggerHelmReleaseReconciliation forces a HelmRelease to reconcile
func (r *FailoverGroupReconciler) triggerHelmReleaseReconciliation(ctx context.Context, name, namespace string) error {
	logger := log.FromContext(ctx).WithValues("helmrelease", name, "namespace", namespace)

	return r.retryOnConflict(ctx, func() error {
		// This is a stub implementation
		// In a real implementation, you would use the Flux HelmRelease API to force reconciliation
		logger.Info("Would trigger HelmRelease reconciliation (stub implementation)")

		// In a real implementation:
		// 1. Get the HelmRelease
		// 2. Add or update the reconcile annotation
		// 3. Update the HelmRelease

		return nil
	})
}

// triggerKustomizationReconciliation forces a Kustomization to reconcile
func (r *FailoverGroupReconciler) triggerKustomizationReconciliation(ctx context.Context, name, namespace string) error {
	logger := log.FromContext(ctx).WithValues("kustomization", name, "namespace", namespace)

	return r.retryOnConflict(ctx, func() error {
		// This is a stub implementation
		// In a real implementation, you would use the Flux Kustomization API to force reconciliation
		logger.Info("Would trigger Kustomization reconciliation (stub implementation)")

		// In a real implementation:
		// 1. Get the Kustomization
		// 2. Add or update the reconcile annotation
		// 3. Update the Kustomization

		return nil
	})
}

// resumeFluxResources resumes the Flux resources in the parent list
func (r *FailoverGroupReconciler) resumeFluxResources(ctx context.Context, resources []crdv1alpha1.ResourceRef, namespace string) error {
	logger := log.FromContext(ctx).WithValues("action", "resumeFluxResources", "namespace", namespace)

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
			logger.Info("Unsupported Flux resource kind, skipping", "kind", resource.Kind)
		}
	}

	return nil
}

// resumeHelmRelease resumes a HelmRelease resource
func (r *FailoverGroupReconciler) resumeHelmRelease(ctx context.Context, name, namespace string) error {
	logger := log.FromContext(ctx).WithValues("helmrelease", name, "namespace", namespace)

	return r.retryOnConflict(ctx, func() error {
		// This is a stub implementation
		// In a real implementation, you would use the Flux HelmRelease API to resume the resource
		logger.Info("Would resume HelmRelease (stub implementation)")

		// In a real implementation:
		// 1. Get the HelmRelease
		// 2. Set .spec.suspend = false
		// 3. Update the HelmRelease

		return nil
	})
}

// resumeKustomization resumes a Kustomization resource
func (r *FailoverGroupReconciler) resumeKustomization(ctx context.Context, name, namespace string) error {
	logger := log.FromContext(ctx).WithValues("kustomization", name, "namespace", namespace)

	return r.retryOnConflict(ctx, func() error {
		// This is a stub implementation
		// In a real implementation, you would use the Flux Kustomization API to resume the resource
		logger.Info("Would resume Kustomization (stub implementation)")

		// In a real implementation:
		// 1. Get the Kustomization
		// 2. Set .spec.suspend = false
		// 3. Update the Kustomization

		return nil
	})
}

// checkForActiveFailovers checks if any Failover resources reference this FailoverGroup
func (r *FailoverGroupReconciler) checkForActiveFailovers(ctx context.Context, failoverGroup *crdv1alpha1.FailoverGroup) (bool, error) {
	logger := log.FromContext(ctx).WithValues("failovergroup", fmt.Sprintf("%s/%s", failoverGroup.Namespace, failoverGroup.Name))

	// List all Failover resources in the cluster
	failoverList := &crdv1alpha1.FailoverList{}
	if err := r.List(ctx, failoverList); err != nil {
		logger.Error(err, "Failed to list Failover resources")
		return false, err
	}

	groupKey := fmt.Sprintf("%s/%s", failoverGroup.Namespace, failoverGroup.Name)

	// Check each Failover to see if it references this FailoverGroup
	for _, failover := range failoverList.Items {
		// Skip Failovers that are completed or failed
		if failover.Status.Status == "SUCCESS" || failover.Status.Status == "FAILED" {
			continue
		}

		// Check each FailoverGroup in this Failover
		for _, group := range failover.Spec.FailoverGroups {
			// Determine the namespace of the group
			namespace := group.Namespace
			if namespace == "" {
				namespace = failover.Namespace
			}

			// Create a key for the referenced group
			refGroupKey := fmt.Sprintf("%s/%s", namespace, group.Name)

			// If this Failover references our FailoverGroup
			if refGroupKey == groupKey {
				logger.Info("Found active Failover referencing this FailoverGroup",
					"failover", fmt.Sprintf("%s/%s", failover.Namespace, failover.Name),
					"status", failover.Status.Status)
				return true, nil
			}
		}
	}

	// No active Failovers found referencing this FailoverGroup
	return false, nil
}

// retryOnConflict attempts to run a function with exponential backoff retry on conflict errors
func (r *FailoverGroupReconciler) retryOnConflict(ctx context.Context, operation func() error) error {
	logger := log.FromContext(ctx)
	maxRetries := 5
	retryDelay := 200 * time.Millisecond

	var lastErr error
	for i := 0; i < maxRetries; i++ {
		err := operation()
		if err == nil {
			return nil // Success
		}

		lastErr = err

		// Check if it's a conflict error
		if errors.IsConflict(err) {
			logger.Info("Conflict error during resource operation, retrying",
				"attempt", i+1, "maxRetries", maxRetries, "error", err.Error())

			// Wait before retry with exponential backoff
			time.Sleep(retryDelay)
			retryDelay = retryDelay * 2
			continue
		}

		// Not a conflict error, return it immediately
		return err
	}

	return fmt.Errorf("operation failed after %d retries, last error: %v", maxRetries, lastErr)
}

// scaleWorkload scales a workload to the specified replica count with retry for conflicts
func (r *FailoverGroupReconciler) scaleWorkload(ctx context.Context, kind, name, namespace string, replicas int32) error {
	logger := log.FromContext(ctx).WithValues("kind", kind, "name", name, "namespace", namespace, "replicas", replicas)

	return r.retryOnConflict(ctx, func() error {
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
	})
}

// scaleDeployment scales a deployment to the specified replica count
func (r *FailoverGroupReconciler) scaleDeployment(ctx context.Context, name, namespace string, replicas int32) error {
	logger := log.FromContext(ctx).WithValues("type", "Deployment", "name", name, "namespace", namespace, "replicas", replicas)

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
func (r *FailoverGroupReconciler) scaleStatefulSet(ctx context.Context, name, namespace string, replicas int32) error {
	logger := log.FromContext(ctx).WithValues("type", "StatefulSet", "name", name, "namespace", namespace, "replicas", replicas)

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

// scaleCronJob suspends/unsuspends a cronjob
func (r *FailoverGroupReconciler) scaleCronJob(ctx context.Context, name, namespace string, suspended bool) error {
	logger := log.FromContext(ctx).WithValues("type", "CronJob", "name", name, "namespace", namespace, "suspended", suspended)

	// Get the cronjob
	cronjob := &batchv1.CronJob{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, cronjob); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("CronJob not found")
			return nil
		}
		return err
	}

	// Set suspended
	cronjob.Spec.Suspend = &suspended

	// Update the cronjob
	logger.Info("Updating cronjob suspend state")
	return r.Update(ctx, cronjob)
}
