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

	crdv1alpha1 "github.com/christensenjairus/Failover-Operator/api/v1alpha1"
	"github.com/christensenjairus/Failover-Operator/internal/controller/cronjobs"
	"github.com/christensenjairus/Failover-Operator/internal/controller/deployments"
	"github.com/christensenjairus/Failover-Operator/internal/controller/dynamodb"
	"github.com/christensenjairus/Failover-Operator/internal/controller/helmreleases"
	"github.com/christensenjairus/Failover-Operator/internal/controller/ingresses"
	"github.com/christensenjairus/Failover-Operator/internal/controller/jobs"
	"github.com/christensenjairus/Failover-Operator/internal/controller/kustomizations"
	"github.com/christensenjairus/Failover-Operator/internal/controller/statefulsets"
	"github.com/christensenjairus/Failover-Operator/internal/controller/virtualservices"
	"github.com/christensenjairus/Failover-Operator/internal/controller/volumereplications"
	"github.com/go-logr/logr"
)

// FailoverReconciler reconciles a Failover object
type FailoverReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Log         logr.Logger
	ClusterName string

	// Managers for different resources
	DeploymentsManager        *deployments.Manager
	StatefulSetsManager       *statefulsets.Manager
	CronJobsManager           *cronjobs.Manager
	JobsManager               *jobs.Manager
	KustomizationsManager     *kustomizations.Manager
	HelmReleasesManager       *helmreleases.Manager
	VirtualServicesManager    *virtualservices.Manager
	VolumeReplicationsManager *volumereplications.Manager
	DynamoDBManager           *dynamodb.Manager
	IngressesManager          *ingresses.Manager
}

//+kubebuilder:rbac:groups=crd.hahomelabs.com,resources=failovers,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=crd.hahomelabs.com,resources=failovers/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=crd.hahomelabs.com,resources=failovers/finalizers,verbs=update
//+kubebuilder:rbac:groups=crd.hahomelabs.com,resources=failovergroups,verbs=get;list;watch;update;patch

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

	// TODO: Initialize status if it's empty
	// TODO: Check if another failover is in progress (for non-emergency types)
	// TODO: Process each FailoverGroup
	// TODO: Handle metrics and status updates

	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}

// handleDeletion handles cleanup when a Failover resource is being deleted
func (r *FailoverReconciler) handleDeletion(ctx context.Context, failover *crdv1alpha1.Failover) (ctrl.Result, error) {
	// TODO: Implement cleanup logic

	// Remove the finalizer
	controllerutil.RemoveFinalizer(failover, "failover.hahomelabs.com/finalizer")
	if err := r.Update(ctx, failover); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *FailoverReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Initialize managers
	r.DeploymentsManager = deployments.NewManager(r.Client)
	r.StatefulSetsManager = statefulsets.NewManager(r.Client)
	r.CronJobsManager = cronjobs.NewManager(r.Client)
	r.JobsManager = jobs.NewManager(r.Client)
	r.KustomizationsManager = kustomizations.NewManager(r.Client)
	r.HelmReleasesManager = helmreleases.NewManager(r.Client)
	r.VirtualServicesManager = virtualservices.NewManager(r.Client)
	r.VolumeReplicationsManager = volumereplications.NewManager(r.Client)
	r.DynamoDBManager = dynamodb.NewManager(r.Client)
	r.IngressesManager = ingresses.NewManager(r.Client)

	return ctrl.NewControllerManagedBy(mgr).
		For(&crdv1alpha1.Failover{}).
		Complete(r)
}
