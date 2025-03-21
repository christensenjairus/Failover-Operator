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
)

// FailoverGroupReconciler reconciles a FailoverGroup object
type FailoverGroupReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Log    logr.Logger

	// Managers will be initialized in SetupWithManager
	// We'll use the same managers from the refactored controller
}

//+kubebuilder:rbac:groups=crd.hahomelabs.com,resources=failovergroups,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=crd.hahomelabs.com,resources=failovergroups/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=crd.hahomelabs.com,resources=failovergroups/finalizers,verbs=update

// Reconcile handles the main reconciliation logic for FailoverGroup
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

	// TODO: Initialize status if empty
	// TODO: Process components to update health status
	// TODO: Verify workload states based on current FailoverGroup state

	// Update health check frequency to every 5 seconds
	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}

// handleDeletion handles the cleanup when a FailoverGroup is being deleted
func (r *FailoverGroupReconciler) handleDeletion(ctx context.Context, failoverGroup *crdv1alpha1.FailoverGroup) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Handling deletion for FailoverGroup", "name", failoverGroup.Name)

	// TODO: Cleanup logic for FailoverGroup deletion

	// Remove finalizer
	controllerutil.RemoveFinalizer(failoverGroup, "failovergroup.hahomelabs.com/finalizer")
	if err := r.Update(ctx, failoverGroup); err != nil {
		logger.Error(err, "Failed to remove finalizer from FailoverGroup")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *FailoverGroupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// We'll reuse the managers from the FailoverReconciler
	// Note: The FailoverReconciler should be set up first

	return ctrl.NewControllerManagedBy(mgr).
		For(&crdv1alpha1.FailoverGroup{}).
		// Ignore status-only updates to reduce reconciliation load
		WithEventFilter(predicate.GenerationChangedPredicate{}).
		Complete(r)
}
