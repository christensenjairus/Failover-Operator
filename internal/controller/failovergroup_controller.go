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

		if err := r.Status().Update(ctx, failoverGroup); err != nil {
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
		if err := r.Status().Update(ctx, failoverGroup); err != nil {
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
	case string(crdv1alpha1.FailoverGroupStateFailover):
		// Group is transitioning from STANDBY to PRIMARY
		logger.Info("FailoverGroup is in FAILOVER state - transitioning from STANDBY to PRIMARY")
		// Reconcile more frequently during state transitions
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	case string(crdv1alpha1.FailoverGroupStateFailback):
		// Group is transitioning from PRIMARY to STANDBY
		logger.Info("FailoverGroup is in FAILBACK state - transitioning from PRIMARY to STANDBY")
		// Reconcile more frequently during state transitions
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	default:
		// Unknown state
		logger.Info("FailoverGroup is in unknown state", "state", failoverGroup.Status.State)
	}

	// Requeue periodically to ensure health stays current
	// More frequent reconciliation for groups in transitory states
	if failoverGroup.Status.State == string(crdv1alpha1.FailoverGroupStateFailover) ||
		failoverGroup.Status.State == string(crdv1alpha1.FailoverGroupStateFailback) {
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
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
	if fg.Status.State == string(crdv1alpha1.FailoverGroupStateFailover) ||
		fg.Status.State == string(crdv1alpha1.FailoverGroupStateFailback) {
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
			fg.Status.State == string(crdv1alpha1.FailoverGroupStateFailover) {
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

// SetupWithManager sets up the controller with the Manager.
func (r *FailoverGroupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&crdv1alpha1.FailoverGroup{}).
		WithEventFilter(predicate.GenerationChangedPredicate{}). // Only reconcile on spec changes
		Complete(r)
}
