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
	"github.com/christensenjairus/Failover-Operator/internal/controller/kustomizations"
	"github.com/christensenjairus/Failover-Operator/internal/controller/statefulsets"
	"github.com/christensenjairus/Failover-Operator/internal/controller/virtualservices"
	"github.com/christensenjairus/Failover-Operator/internal/controller/volumereplications"
	"github.com/go-logr/logr"
)

// FailoverReconciler reconciles a Failover object
// It manages the process of executing failover operations for FailoverGroups
type FailoverReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Log         logr.Logger
	ClusterName string // The name of the current Kubernetes cluster

	// Resource Managers handle specific resource types during failover operations
	DeploymentsManager        *deployments.Manager        // Manages Deployment scaling operations
	StatefulSetsManager       *statefulsets.Manager       // Manages StatefulSet scaling operations
	CronJobsManager           *cronjobs.Manager           // Manages CronJob suspension operations
	KustomizationsManager     *kustomizations.Manager     // Manages FluxCD Kustomization reconciliation
	HelmReleasesManager       *helmreleases.Manager       // Manages FluxCD HelmRelease reconciliation
	VirtualServicesManager    *virtualservices.Manager    // Manages Istio VirtualService traffic routing
	VolumeReplicationsManager *volumereplications.Manager // Manages Rook VolumeReplication state changes
	DynamoDBManager           *dynamodb.Manager           // Manages DynamoDB state coordination and locking
	IngressesManager          *ingresses.Manager          // Manages Kubernetes Ingress configuration
}

//+kubebuilder:rbac:groups=crd.hahomelabs.com,resources=failovers,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=crd.hahomelabs.com,resources=failovers/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=crd.hahomelabs.com,resources=failovers/finalizers,verbs=update
//+kubebuilder:rbac:groups=crd.hahomelabs.com,resources=failovergroups,verbs=get;list;watch;update;patch

// Reconcile handles the main reconciliation logic for Failover
// This is the main controller loop that processes Failover requests
func (r *FailoverReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("failover", req.NamespacedName)
	r.Log = logger

	// Fetch the Failover instance that triggered this reconciliation
	failover := &crdv1alpha1.Failover{}
	err := r.Get(ctx, req.NamespacedName, failover)
	if err != nil {
		if errors.IsNotFound(err) {
			// Object not found, might have been deleted after reconcile request.
			// Return and don't requeue
			logger.Info("Failover resource not found. Ignoring since it was deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue with error
		logger.Error(err, "Failed to get Failover resource")
		return ctrl.Result{}, err
	}

	// Add finalizer if it doesn't exist
	// This ensures we can handle cleanup when the Failover is deleted
	if !controllerutil.ContainsFinalizer(failover, "failover.hahomelabs.com/finalizer") {
		logger.Info("Adding finalizer to Failover")
		controllerutil.AddFinalizer(failover, "failover.hahomelabs.com/finalizer")
		if err := r.Update(ctx, failover); err != nil {
			logger.Error(err, "Failed to add finalizer to Failover")
			return ctrl.Result{}, err
		}
		// We've updated the object, so we should requeue and process again
		return ctrl.Result{Requeue: true}, nil
	}

	// Handle deletion if marked for deletion
	if !failover.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, failover)
	}

	// TODO: Initialize status if it's empty
	// This would set initial values for tracking progress and health

	// TODO: Check if another failover is in progress (for non-emergency types)
	// For non-emergency failovers, we should prevent concurrent operations

	// TODO: Process each FailoverGroup
	// For each FailoverGroup:
	// 1. Verify prerequisites (source and target clusters, resource health)
	// 2. Lock the FailoverGroup in DynamoDB
	// 3. Execute failover workflow:
	//    a. Update VolumeReplication direction
	//    b. Wait for data synchronization
	//    c. Scale down applications in source cluster
	//    d. Scale up applications in target cluster
	//    e. Update virtualservices/ingresses to point to target cluster
	// 4. Update status with progress and results
	// 5. Release lock

	// TODO: Handle metrics and status updates
	// Record performance metrics (downtime, total operation time)
	// Update final status with results and timestamp

	// Requeue to check progress and status
	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}

// handleDeletion handles cleanup when a Failover resource is being deleted
// This ensures proper cleanup of resources associated with the Failover
func (r *FailoverReconciler) handleDeletion(ctx context.Context, failover *crdv1alpha1.Failover) (ctrl.Result, error) {
	logger := r.Log.WithValues("phase", "deletion")
	logger.Info("Handling deletion of Failover resource")

	// TODO: Implement cleanup logic
	// This could include:
	// 1. Release any locks held by this failover operation
	// 2. Mark in-progress operations as cancelled
	// 3. Cleanup temporary resources created during the failover
	// 4. Record the cancellation in the failover history
	logger.Info("Performing cleanup for Failover deletion")

	// Remove the finalizer to allow Kubernetes to complete deletion
	logger.Info("Removing finalizer from Failover")
	controllerutil.RemoveFinalizer(failover, "failover.hahomelabs.com/finalizer")
	if err := r.Update(ctx, failover); err != nil {
		logger.Error(err, "Failed to remove finalizer from Failover")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
// This initializes all the resource managers and registers the controller
func (r *FailoverReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Log.Info("Setting up FailoverReconciler with controllers")

	// Initialize resource managers with the Kubernetes client
	// Each manager handles a specific type of resource during failover operations
	r.DeploymentsManager = deployments.NewManager(r.Client)
	r.StatefulSetsManager = statefulsets.NewManager(r.Client)
	r.CronJobsManager = cronjobs.NewManager(r.Client)
	r.KustomizationsManager = kustomizations.NewManager(r.Client)
	r.HelmReleasesManager = helmreleases.NewManager(r.Client)
	r.VirtualServicesManager = virtualservices.NewManager(r.Client)
	r.VolumeReplicationsManager = volumereplications.NewManager(r.Client)
	r.IngressesManager = ingresses.NewManager(r.Client)

	// Note: DynamoDBManager initialization requires additional AWS configuration
	// This would typically be handled separately using AWS credentials
	// r.DynamoDBManager = dynamodb.NewManager(awsDynamoClient, tableName, r.OperatorID, r.ClusterName)

	// Register this controller for Failover resources
	return ctrl.NewControllerManagedBy(mgr).
		For(&crdv1alpha1.Failover{}).
		Complete(r)
}
