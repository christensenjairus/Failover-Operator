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

// FailoverGroupReconciler reconciles a FailoverGroup object
// It manages the health monitoring and state maintenance of FailoverGroups
type FailoverGroupReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Log         logr.Logger
	ClusterName string // The name of the current Kubernetes cluster

	// Resource Managers handle specific resource types during health checks and state maintenance
	DeploymentsManager        *deployments.Manager        // Manages Deployment health checks
	StatefulSetsManager       *statefulsets.Manager       // Manages StatefulSet health checks
	CronJobsManager           *cronjobs.Manager           // Manages CronJob health checks
	KustomizationsManager     *kustomizations.Manager     // Manages FluxCD Kustomization health checks
	HelmReleasesManager       *helmreleases.Manager       // Manages FluxCD HelmRelease health checks
	VirtualServicesManager    *virtualservices.Manager    // Manages Istio VirtualService configuration
	VolumeReplicationsManager *volumereplications.Manager // Manages Rook VolumeReplication health checks
	DynamoDBManager           *dynamodb.DynamoDBService   // Manages DynamoDB state coordination and heartbeats
	IngressesManager          *ingresses.Manager          // Manages Kubernetes Ingress health checks
}

//+kubebuilder:rbac:groups=crd.hahomelabs.com,resources=failovergroups,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=crd.hahomelabs.com,resources=failovergroups/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=crd.hahomelabs.com,resources=failovergroups/finalizers,verbs=update

// Reconcile handles the main reconciliation logic for FailoverGroup
// This is the main controller loop that processes FailoverGroup resources
func (r *FailoverGroupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("failovergroup", req.NamespacedName)
	r.Log = logger

	// Fetch the FailoverGroup instance that triggered this reconciliation
	failoverGroup := &crdv1alpha1.FailoverGroup{}
	if err := r.Get(ctx, req.NamespacedName, failoverGroup); err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Don't requeue
			logger.Info("FailoverGroup resource not found. Ignoring since it was deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue with error.
		logger.Error(err, "Failed to get FailoverGroup resource")
		return ctrl.Result{}, err
	}

	// Add finalizer if it doesn't exist
	// This ensures we can handle cleanup when the FailoverGroup is deleted
	if !controllerutil.ContainsFinalizer(failoverGroup, "failovergroup.hahomelabs.com/finalizer") {
		logger.Info("Adding finalizer to FailoverGroup")
		controllerutil.AddFinalizer(failoverGroup, "failovergroup.hahomelabs.com/finalizer")
		if err := r.Update(ctx, failoverGroup); err != nil {
			logger.Error(err, "Failed to add finalizer to FailoverGroup")
			return ctrl.Result{}, err
		}
		// We've updated the object, so we should requeue and process again
		return ctrl.Result{Requeue: true}, nil
	}

	// Handle deletion if marked for deletion
	if !failoverGroup.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, failoverGroup)
	}

	// TODO: Initialize status if empty
	// This would set initial values for tracking state and health
	if failoverGroup.Status.State == "" {
		logger.Info("Initializing FailoverGroup status")
		// Initialize with STANDBY state by default
		// This will be updated to PRIMARY when appropriate
		failoverGroup.Status.State = string(crdv1alpha1.FailoverGroupStateStandby)
		if err := r.Status().Update(ctx, failoverGroup); err != nil {
			logger.Error(err, "Failed to initialize FailoverGroup status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// TODO: Sync with DynamoDB
	// 1. Get ownership information from DynamoDB
	// 2. Update local status.globalState based on DynamoDB records
	// 3. Update DynamoDB heartbeat for this cluster
	logger.V(1).Info("Syncing FailoverGroup state with DynamoDB")

	// TODO: Process components to update health status
	// For each component in the FailoverGroup:
	// 1. Check health of all resources (deployments, statefulsets, etc.)
	// 2. Update component status in FailoverGroup status
	// 3. Report component health to DynamoDB via heartbeat
	logger.V(1).Info("Checking component health")

	// TODO: Verify workload states based on current FailoverGroup state
	// If this cluster is PRIMARY:
	//   - Ensure all workloads are scaled up
	//   - Ensure all VirtualServices route traffic here
	//   - Ensure all Ingresses are active
	// If this cluster is STANDBY:
	//   - Ensure all workloads are scaled down
	//   - Ensure all VirtualServices route traffic elsewhere
	//   - Ensure all Ingresses are passive
	logger.V(1).Info("Verifying workload states match FailoverGroup state")

	// Update health check frequency to every 5 seconds
	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}

// handleDeletion handles the cleanup when a FailoverGroup is being deleted
// This ensures proper cleanup of resources associated with the FailoverGroup
func (r *FailoverGroupReconciler) handleDeletion(ctx context.Context, failoverGroup *crdv1alpha1.FailoverGroup) (ctrl.Result, error) {
	logger := r.Log.WithValues("phase", "deletion")
	logger.Info("Handling deletion for FailoverGroup", "name", failoverGroup.Name)

	// TODO: Cleanup logic for FailoverGroup deletion
	// This could include:
	// 1. Remove ownership records from DynamoDB
	// 2. Remove heartbeat records for this cluster
	// 3. Remove configuration records
	// 4. Release any locks held by this FailoverGroup
	// 5. Clean up any remaining resources
	logger.Info("Performing cleanup for FailoverGroup deletion")

	// Remove finalizer to allow Kubernetes to complete deletion
	logger.Info("Removing finalizer from FailoverGroup")
	controllerutil.RemoveFinalizer(failoverGroup, "failovergroup.hahomelabs.com/finalizer")
	if err := r.Update(ctx, failoverGroup); err != nil {
		logger.Error(err, "Failed to remove finalizer from FailoverGroup")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
// This initializes all the resource managers and registers the controller
func (r *FailoverGroupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Log.Info("Setting up FailoverGroupReconciler with controllers")

	// We'll reuse the managers from the FailoverReconciler if they're shared
	// Otherwise, initialize new managers here
	if r.DeploymentsManager == nil {
		r.DeploymentsManager = deployments.NewManager(r.Client)
	}
	if r.StatefulSetsManager == nil {
		r.StatefulSetsManager = statefulsets.NewManager(r.Client)
	}
	if r.CronJobsManager == nil {
		r.CronJobsManager = cronjobs.NewManager(r.Client)
	}
	if r.KustomizationsManager == nil {
		r.KustomizationsManager = kustomizations.NewManager(r.Client)
	}
	if r.HelmReleasesManager == nil {
		r.HelmReleasesManager = helmreleases.NewManager(r.Client)
	}
	if r.VirtualServicesManager == nil {
		r.VirtualServicesManager = virtualservices.NewManager(r.Client)
	}
	if r.VolumeReplicationsManager == nil {
		r.VolumeReplicationsManager = volumereplications.NewManager(r.Client)
	}
	if r.IngressesManager == nil {
		r.IngressesManager = ingresses.NewManager(r.Client)
	}

	// Note: DynamoDBManager initialization requires additional AWS configuration
	// This is a placeholder - in an actual implementation, we would:
	// 1. Get AWS credentials from environment variables or secrets
	// 2. Set up a DynamoDB client
	// 3. Configure the table name based on configuration
	// 4. Generate a unique operator ID if not provided
	// Example:
	//
	// if r.DynamoDBManager == nil {
	//     tableName := os.Getenv("DYNAMODB_TABLE_NAME")
	//     if tableName == "" {
	//         tableName = "failover-operator"
	//     }
	//
	//     operatorID := os.Getenv("OPERATOR_ID")
	//     if operatorID == "" {
	//         // Generate a unique ID
	//         operatorID = uuid.New().String()
	//     }
	//
	//     awsConfig, err := config.LoadDefaultConfig(context.Background())
	//     if err != nil {
	//         return err
	//     }
	//
	//     dynamoClient := dynamodb.NewFromConfig(awsConfig)
	//     r.DynamoDBManager = dynamodb.NewManager(dynamoClient, tableName, operatorID, r.ClusterName)
	// }

	// Register this controller for FailoverGroup resources
	// Use predicate to ignore status-only updates to reduce reconciliation load
	return ctrl.NewControllerManagedBy(mgr).
		For(&crdv1alpha1.FailoverGroup{}).
		WithEventFilter(predicate.GenerationChangedPredicate{}).
		Complete(r)
}
