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

	crdv1alpha1 "github.com/christensenjairus/Failover-Operator/api/v1alpha1"
	"github.com/christensenjairus/Failover-Operator/internal/controller/dynamodb"
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

	// Handle deletion if needed
	if !failoverGroup.ObjectMeta.DeletionTimestamp.IsZero() {
		log.Info("FailoverGroup is being deleted")
		if err := r.handleDeletion(ctx, failoverGroup); err != nil {
			log.Error(err, "Failed to handle deletion")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Add finalizer if it doesn't exist
	if !controllerutil.ContainsFinalizer(failoverGroup, finalizerName) {
		log.Info("Adding finalizer to FailoverGroup")
		controllerutil.AddFinalizer(failoverGroup, finalizerName)
		if err := r.Update(ctx, failoverGroup); err != nil {
			log.Error(err, "Failed to add finalizer")
			return ctrl.Result{}, err
		}
		// Return and requeue to continue with normal reconciliation
		return ctrl.Result{Requeue: true}, nil
	}

	// Update the suspended status to match the spec
	if failoverGroup.Status.Suspended != failoverGroup.Spec.Suspended {
		log.Info("Updating suspended status to match spec",
			"current", failoverGroup.Status.Suspended,
			"new", failoverGroup.Spec.Suspended)

		// Make a copy to avoid modifying the shared instance
		fgCopy := failoverGroup.DeepCopy()
		fgCopy.Status.Suspended = fgCopy.Spec.Suspended

		if err := r.Status().Update(ctx, fgCopy); err != nil {
			log.Error(err, "Failed to update suspended status")
			return ctrl.Result{}, err
		}

		// Update our local copy
		failoverGroup.Status.Suspended = fgCopy.Status.Suspended

		// Requeue to continue with remaining logic
		return ctrl.Result{Requeue: true}, nil
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

	// Clean up stale cluster statuses
	if err := r.Manager.CleanupStaleClusterStatuses(ctx, failoverGroup); err != nil {
		log.Error(err, "Failed to clean up stale cluster statuses")
		// Continue anyway
	}

	// Check and handle any ongoing failover operation stages
	handled, result, err := r.checkAndHandleFailoverStages(ctx, failoverGroup)
	if handled {
		return result, err
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

// handleDeletion implements finalizer logic for FailoverGroup to ensure proper cleanup
// When a FailoverGroup is deleted, this method:
// 1. Checks if this cluster is the primary and transfers ownership to another healthy cluster if possible
// 2. Releases any locks held by this cluster
// 3. Cleans up resources in DynamoDB for this group, including:
//   - Configuration records
//   - History records
//   - Cluster status records
//   - Lock records
//   - Volume state information
//
// 4. Removes the finalizer to allow Kubernetes to delete the object
//
// This ensures that DynamoDB resources are properly cleaned up when a FailoverGroup is deleted.
// The finalizer prevents Kubernetes from removing the FailoverGroup until all this cleanup is complete.
func (r *FailoverGroupReconciler) handleDeletion(ctx context.Context, fg *crdv1alpha1.FailoverGroup) error {
	// Check if our finalizer is present
	if !controllerutil.ContainsFinalizer(fg, finalizerName) {
		return nil
	}

	// Perform cleanup
	logger := log.FromContext(ctx).WithValues("failovergroup", fg.Name, "namespace", fg.Namespace)
	logger.Info("Processing deletion")

	dynamoDB := r.Manager.DynamoDBManager
	baseManager := dynamoDB.BaseManager
	operationsManager := dynamodb.NewOperationsManager(baseManager)

	clusterName := r.ClusterName
	namespace := fg.Namespace
	name := fg.Name

	// Get all cluster statuses to see if there are any other clusters
	statuses, err := dynamoDB.GetAllClusterStatuses(ctx, namespace, name)
	if err != nil {
		logger.Error(err, "Failed to get cluster statuses")
		// Continue with deletion even if we can't get statuses
	}

	// Determine if this is the last cluster with a status
	isLastCluster := statuses == nil || len(statuses) <= 1
	logger.Info("Checking if this is the last cluster", "isLastCluster", isLastCluster, "statusCount", len(statuses))

	// Attempt to get the group configuration record
	groupConfig, err := dynamoDB.GetGroupConfig(ctx, namespace, name)
	if err != nil {
		logger.Error(err, "Failed to get group configuration")
		// Continue with deletion even if we can't get the config
	}

	// Determine if this cluster is the primary/owner
	isPrimary := false
	if groupConfig != nil && groupConfig.OwnerCluster == clusterName {
		isPrimary = true
		logger.Info("This cluster is the primary owner in configuration")
	}

	// Handle cleanup based on role and whether this is the last cluster
	if isPrimary {
		if isLastCluster {
			// This is the primary and the only cluster, clean up everything
			logger.Info("This is the primary and only remaining cluster, cleaning up all DynamoDB records")

			// Delete the GroupConfigRecord
			err = baseManager.DeleteGroupConfig(ctx, namespace, name)
			if err != nil {
				logger.Error(err, "Failed to delete group configuration")
			} else {
				logger.Info("Successfully deleted group configuration")
			}

			// Delete all history records
			err = baseManager.DeleteAllHistoryRecords(ctx, namespace, name)
			if err != nil {
				logger.Error(err, "Failed to delete history records")
			} else {
				logger.Info("Successfully deleted all history records")
			}

			// Delete lock record
			err = baseManager.DeleteLock(ctx, namespace, name)
			if err != nil {
				logger.Error(err, "Failed to delete lock record")
			} else {
				logger.Info("Successfully deleted lock record")
			}

			// Delete all cluster statuses
			err = baseManager.DeleteAllClusterStatuses(ctx, namespace, name)
			if err != nil {
				logger.Error(err, "Failed to delete all cluster statuses")
			} else {
				logger.Info("Successfully deleted all cluster statuses")
			}
		} else {
			// Multiple clusters exist, try to transfer ownership to another healthy cluster
			logger.Info("Multiple clusters exist, attempting to transfer ownership")
			transferredOwnership := false
			for otherCluster, status := range statuses {
				if otherCluster != clusterName && status.Health == "OK" {
					logger.Info("Transferring ownership to another cluster", "targetCluster", otherCluster)
					err := operationsManager.TransferOwnership(ctx, namespace, name, otherCluster)
					if err != nil {
						logger.Error(err, "Failed to transfer ownership", "targetCluster", otherCluster)
					} else {
						logger.Info("Successfully transferred ownership", "targetCluster", otherCluster)
						transferredOwnership = true
						break
					}
				}
			}

			// If ownership transfer failed for all clusters, remove the group config
			if !transferredOwnership {
				logger.Info("Could not transfer ownership, cleaning up group configuration")
				// Delete the GroupConfigRecord
				err = baseManager.DeleteGroupConfig(ctx, namespace, name)
				if err != nil {
					logger.Error(err, "Failed to delete group configuration")
				} else {
					logger.Info("Successfully deleted group configuration")
				}
			}

			// Remove this cluster's status
			logger.Info("Removing this cluster's status from DynamoDB")
			err = operationsManager.RemoveClusterStatus(ctx, namespace, name, clusterName)
			if err != nil {
				logger.Error(err, "Failed to remove cluster status")
			} else {
				logger.Info("Successfully removed cluster status")
			}
		}
	} else {
		// Not the primary cluster
		if isLastCluster {
			// This is the last cluster but not primary, clean up everything
			logger.Info("This is the last remaining cluster but not the primary, cleaning up all DynamoDB records")

			// Delete the GroupConfigRecord
			err = baseManager.DeleteGroupConfig(ctx, namespace, name)
			if err != nil {
				logger.Error(err, "Failed to delete group configuration")
			}

			// Delete all history records
			err = baseManager.DeleteAllHistoryRecords(ctx, namespace, name)
			if err != nil {
				logger.Error(err, "Failed to delete history records")
			}

			// Delete lock record
			err = baseManager.DeleteLock(ctx, namespace, name)
			if err != nil {
				logger.Error(err, "Failed to delete lock record")
			}

			// Delete all cluster statuses
			err = baseManager.DeleteAllClusterStatuses(ctx, namespace, name)
			if err != nil {
				logger.Error(err, "Failed to delete all cluster statuses")
			}
		} else {
			// Just remove this cluster's status
			logger.Info("Removing this cluster's status from DynamoDB")
			err = operationsManager.RemoveClusterStatus(ctx, namespace, name, clusterName)
			if err != nil {
				logger.Error(err, "Failed to remove cluster status")
			}
		}
	}

	// Release any locks held by this cluster
	lockAcquired, leaseToken, err := dynamoDB.IsLocked(ctx, namespace, name)
	if err != nil {
		logger.Error(err, "Failed to check lock status")
	} else if lockAcquired {
		logger.Info("Releasing lock")
		err = dynamoDB.ReleaseLock(ctx, namespace, name, leaseToken)
		if err != nil {
			logger.Error(err, "Failed to release lock")
		}
	}

	// Remove volume state if this was the primary cluster
	err = dynamoDB.RemoveVolumeState(ctx, namespace, name)
	if err != nil {
		logger.Error(err, "Failed to remove volume state")
	}

	// Remove finalizer to allow Kubernetes to delete the object
	controllerutil.RemoveFinalizer(fg, finalizerName)
	err = r.Update(ctx, fg)
	if err != nil {
		return fmt.Errorf("failed to remove finalizer: %w", err)
	}

	logger.Info("Successfully cleaned up FailoverGroup")
	return nil
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
		Complete(r)
}
