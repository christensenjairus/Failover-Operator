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

package failovergroup

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	crdv1alpha1 "github.com/christensenjairus/Failover-Operator/api/v1alpha1"
)

const finalizerName = "failovergroup.failover-operator.io/finalizer"

// FailoverGroupReconciler reconciles a FailoverGroup object
type FailoverGroupReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Log         logr.Logger
	ClusterName string

	// Manager for handling FailoverGroup operations
	Manager *Manager

	// For managing the periodic synchronization
	ctx        context.Context
	cancelFunc context.CancelFunc
}

//+kubebuilder:rbac:groups=failover-operator.io,resources=failovergroups,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=failover-operator.io,resources=failovergroups/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=failover-operator.io,resources=failovergroups/finalizers,verbs=update
//+kubebuilder:rbac:groups=apps,resources=deployments;statefulsets,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=batch,resources=cronjobs,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=networking.istio.io,resources=virtualservices,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=kustomize.toolkit.fluxcd.io,resources=kustomizations,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=helm.toolkit.fluxcd.io,resources=helmreleases,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=replication.storage.openshift.io,resources=volumereplications,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups="",resources=secrets;configmaps,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *FailoverGroupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("failovergroup", req.NamespacedName)

	// Get the FailoverGroup resource
	failoverGroup := &crdv1alpha1.FailoverGroup{}
	if err := r.Get(ctx, req.NamespacedName, failoverGroup); err != nil {
		// Handle not-found error
		if errors.IsNotFound(err) {
			log.Info("FailoverGroup resource not found, ignoring")
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get FailoverGroup resource")
		return ctrl.Result{}, err
	}

	// Initialize the manager for this FailoverGroup if needed
	if r.Manager == nil {
		log.Error(nil, "FailoverGroup manager not initialized")
		return ctrl.Result{}, fmt.Errorf("FailoverGroup manager not initialized")
	}

	// Synchronize with DynamoDB to ensure local status reflects global state
	if err := r.Manager.SyncWithDynamoDB(ctx, failoverGroup); err != nil {
		log.Error(err, "Failed to sync with DynamoDB")
		// Continue anyway to update heartbeat and local status
	}

	// Update the heartbeat in DynamoDB
	if err := r.Manager.UpdateHeartbeat(ctx, failoverGroup); err != nil {
		log.Error(err, "Failed to update heartbeat")
		// Continue anyway to update local status
	}

	// Update cluster status in DynamoDB
	if err := r.Manager.UpdateDynamoDBStatus(ctx, failoverGroup); err != nil {
		log.Error(err, "Failed to update status in DynamoDB")
		// Continue anyway
	}

	// Requeue after the heartbeat interval to ensure regular updates
	// For now, use a fixed interval
	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

// checkAndHandleFailoverStages checks for ongoing failover operations
// and handles transitions between stages based on DynamoDB state
func (r *FailoverGroupReconciler) checkAndHandleFailoverStages(
	ctx context.Context,
	failoverGroup *crdv1alpha1.FailoverGroup) (bool, ctrl.Result, error) {

	logger := log.FromContext(ctx).WithValues(
		"namespace", failoverGroup.Namespace,
		"name", failoverGroup.Name,
	)

	// Check if there's an ongoing failover operation that requires action
	volumeState, exists := r.Manager.GetVolumeStateFromDynamoDB(ctx, failoverGroup)
	if !exists {
		// No volume state information in DynamoDB, nothing to do
		return false, ctrl.Result{}, nil
	}

	// If this is the target cluster and volumes are ready for promotion
	if volumeState == "READY_FOR_PROMOTION" &&
		r.ClusterName == failoverGroup.Status.GlobalState.ActiveCluster {

		logger.Info("Detected volumes ready for promotion, handling Volume Promotion Stage (Stage 4)")

		// Handle the volume promotion here, or delegate to the manager
		if err := r.Manager.HandleVolumePromotion(ctx, failoverGroup); err != nil {
			logger.Error(err, "Failed to handle volume promotion")
			return true, ctrl.Result{RequeueAfter: time.Second * 30}, err
		}

		return true, ctrl.Result{RequeueAfter: time.Second * 10}, nil
	}

	// If volumes are promoted and this is the target cluster
	if volumeState == "PROMOTED" &&
		r.ClusterName == failoverGroup.Status.GlobalState.ActiveCluster {

		logger.Info("Detected volumes promoted, handling Target Cluster Activation (Stage 5)")

		// Handle activation here, or delegate to the manager
		if err := r.Manager.HandleTargetActivation(ctx, failoverGroup); err != nil {
			logger.Error(err, "Failed to handle target activation")
			return true, ctrl.Result{RequeueAfter: time.Second * 30}, err
		}

		return true, ctrl.Result{RequeueAfter: time.Second * 10}, nil
	}

	// No action needed for this controller in the current state
	return false, ctrl.Result{}, nil
}

// handleDeletion cleans up resources and removes finalizer
func (r *FailoverGroupReconciler) handleDeletion(ctx context.Context, failoverGroup *crdv1alpha1.FailoverGroup) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues(
		"namespace", failoverGroup.Namespace,
		"name", failoverGroup.Name,
	)

	// Check if the finalizer is still present
	if controllerutil.ContainsFinalizer(failoverGroup, finalizerName) {
		logger.Info("Performing cleanup before deletion")

		// Perform cleanup:
		// 1. Remove from DynamoDB if this is the last cluster
		// 2. Release any locks
		// 3. Scale down any workloads

		// Release any locks if they exist
		if r.Manager.DynamoDBManager != nil {
			// Check if lock exists and release it
			locked, leaseToken, _ := r.Manager.DynamoDBManager.IsLocked(
				ctx, failoverGroup.Namespace, failoverGroup.Name)
			if locked && leaseToken != "" {
				_ = r.Manager.DynamoDBManager.ReleaseLock(
					ctx, failoverGroup.Namespace, failoverGroup.Name, leaseToken)
			}

			// Cleanup any volume state information
			_ = r.Manager.DynamoDBManager.RemoveVolumeState(
				ctx, failoverGroup.Namespace, failoverGroup.Name)
		}

		// Remove the finalizer
		controllerutil.RemoveFinalizer(failoverGroup, finalizerName)
		if err := r.Update(ctx, failoverGroup); err != nil {
			logger.Error(err, "Failed to remove finalizer")
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *FailoverGroupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Initialize the manager if it hasn't been set
	if r.Manager == nil {
		// Create the manager with client and logger
		r.Manager = NewManager(r.Client, r.ClusterName, r.Log.WithName("failovergroup-manager"))

		// Initialize resource managers if needed
		// This would happen if you want to inject specialized or mock implementations
		// for testing or other specific use cases

		// In a production environment, you would configure the DynamoDB service
		// based on your AWS configuration
		// Example:
		// awsConfig := aws.NewConfig().WithRegion("us-west-2")
		// if os.Getenv("AWS_ENDPOINT") != "" {
		//     awsConfig = awsConfig.WithEndpoint(os.Getenv("AWS_ENDPOINT"))
		// }
		// dbSvc := dynamodb.NewDynamoDBService(
		//     awsSession.New(awsConfig),
		//     os.Getenv("DYNAMODB_TABLE"),
		//     r.ClusterName,
		//     os.Getenv("OPERATOR_ID"),
		// )
		// r.FailoverGroupManager.SetDynamoDBManager(dbSvc)
	}

	// Start the periodic synchronization
	r.ctx, r.cancelFunc = context.WithCancel(context.Background())
	r.Manager.StartPeriodicSynchronization(r.ctx, 30) // 30 second intervals

	return ctrl.NewControllerManagedBy(mgr).
		For(&crdv1alpha1.FailoverGroup{}).
		WithEventFilter(predicate.GenerationChangedPredicate{}).
		Complete(r)
}
