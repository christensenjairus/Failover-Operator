package failover

import (
	"context"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	crdv1alpha1 "github.com/christensenjairus/Failover-Operator/api/v1alpha1"
	"github.com/christensenjairus/Failover-Operator/internal/controller/dynamodb"
	"github.com/christensenjairus/Failover-Operator/internal/workflow"
)

const finalizerName = "failover.failover-operator.io/finalizer"

// FailoverReconciler reconciles a Failover object
type FailoverReconciler struct {
	client.Client
	logr.Logger
	Scheme      *runtime.Scheme
	ClusterName string

	// Workflow components
	WorkflowAdapter   *workflow.ManagerAdapter
	DynamoDBManager   *dynamodb.DynamoDBService
	AutoFailoverCheck *AutoFailoverChecker
	FailoverManager   *Manager
	cancelFunc        context.CancelFunc
}

// SetupWithManager sets up the controller with the Manager.
func (r *FailoverReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Create a context for the automatic failover checker
	ctx, cancel := context.WithCancel(context.Background())
	r.cancelFunc = cancel

	// Initialize the workflow adapter if not already initialized
	if r.WorkflowAdapter == nil && r.DynamoDBManager != nil {
		r.WorkflowAdapter = workflow.NewManagerAdapter(r.Client, r.Logger, r.ClusterName, r.DynamoDBManager)
	}

	// Initialize the auto failover checker if not already initialized
	if r.AutoFailoverCheck == nil && r.DynamoDBManager != nil {
		r.AutoFailoverCheck = NewAutoFailoverChecker(r.Client, r.Logger, r.ClusterName, r.DynamoDBManager)
	}

	// Start the automatic failover checker if available
	if r.AutoFailoverCheck != nil {
		r.AutoFailoverCheck.StartChecker(ctx, 30) // Check every 30 seconds
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&crdv1alpha1.Failover{}).
		Complete(r)
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.14.1/pkg/reconcile
func (r *FailoverReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	logger := log.FromContext(ctx).WithValues("failover", req.NamespacedName)

	// Fetch the Failover instance
	failover := &crdv1alpha1.Failover{}
	if err := r.Get(ctx, req.NamespacedName, failover); err != nil {
		if errors.IsNotFound(err) {
			// Object not found, likely deleted
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request
		logger.Error(err, "Failed to get Failover")
		return reconcile.Result{}, err
	}

	// Handle finalizer and deletion
	if !failover.ObjectMeta.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, failover)
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(failover, finalizerName) {
		controllerutil.AddFinalizer(failover, finalizerName)
		if err := r.Update(ctx, failover); err != nil {
			logger.Error(err, "Failed to add finalizer")
			return reconcile.Result{}, err
		}
		return reconcile.Result{Requeue: true}, nil
	}

	// Failover already completed successfully, no need to process again
	if failover.Status.Status == "SUCCESS" {
		logger.Info("Failover already completed successfully")
		return reconcile.Result{}, nil
	}

	// Failover already failed, no automatic retry to avoid cascading issues
	// Requires manual intervention (create a new failover or fix the issue)
	if failover.Status.Status == "FAILED" {
		logger.Info("Failover previously failed, manual intervention required")
		return reconcile.Result{}, nil
	}

	// Process the failover request
	logger.Info("Processing failover",
		"targetCluster", failover.Spec.TargetCluster,
		"mode", failover.Spec.FailoverMode,
		"groups", len(failover.Spec.FailoverGroups))

	// Update status to in progress if not already set
	if failover.Status.Status != "IN_PROGRESS" {
		failover.Status.Status = "IN_PROGRESS"
		if err := r.Status().Update(ctx, failover); err != nil {
			logger.Error(err, "Failed to update failover status to IN_PROGRESS")
			return reconcile.Result{}, err
		}
	}

	// Initialize or update the failover groups status array if needed
	if failover.Status.FailoverGroups == nil || len(failover.Status.FailoverGroups) == 0 {
		failover.Status.FailoverGroups = make([]crdv1alpha1.FailoverGroupReference, len(failover.Spec.FailoverGroups))
		for i, group := range failover.Spec.FailoverGroups {
			failover.Status.FailoverGroups[i] = crdv1alpha1.FailoverGroupReference{
				Name:      group.Name,
				Namespace: group.Namespace,
				Status:    "PENDING",
			}
		}
		if err := r.Status().Update(ctx, failover); err != nil {
			logger.Error(err, "Failed to initialize failover group status array")
			return reconcile.Result{}, err
		}
	}

	// Process each failover group
	for i, groupRef := range failover.Spec.FailoverGroups {
		groupLog := logger.WithValues("failoverGroup", groupRef.Name)

		// Get namespace, defaulting to failover's namespace if not specified
		namespace := groupRef.Namespace
		if namespace == "" {
			namespace = failover.Namespace
		}

		// Fetch the FailoverGroup
		failoverGroup := &crdv1alpha1.FailoverGroup{}
		if err := r.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      groupRef.Name,
		}, failoverGroup); err != nil {
			groupLog.Error(err, "Failed to get failover group")

			// Update status for this group
			failover.Status.FailoverGroups[i].Status = "FAILED"
			failover.Status.FailoverGroups[i].Message = "Failed to get failover group: " + err.Error()
			if updateErr := r.Status().Update(ctx, failover); updateErr != nil {
				logger.Error(updateErr, "Failed to update failover group status")
			}

			continue
		}

		// Check if this operator should process this group
		// Skip if operator ID doesn't match
		if failoverGroup.Spec.OperatorID != "" && r.DynamoDBManager != nil &&
			failoverGroup.Spec.OperatorID != r.DynamoDBManager.OperatorID {
			groupLog.Info("Skipping failover group - operator ID mismatch",
				"groupOperatorID", failoverGroup.Spec.OperatorID,
				"thisOperatorID", r.DynamoDBManager.OperatorID)
			continue
		}

		// Skip if already processed
		if failover.Status.FailoverGroups[i].Status == "SUCCESS" {
			groupLog.Info("Group already failed over successfully")
			continue
		}

		// Skip if failed
		if failover.Status.FailoverGroups[i].Status == "FAILED" {
			groupLog.Info("Group previously failed, skipping")
			continue
		}

		// Update status to in progress
		failover.Status.FailoverGroups[i].Status = "IN_PROGRESS"
		if err := r.Status().Update(ctx, failover); err != nil {
			groupLog.Error(err, "Failed to update group status to IN_PROGRESS")
			return reconcile.Result{}, err
		}

		// Process the failover for this group using the workflow adapter
		groupLog.Info("Processing failover for group",
			"mode", failover.Spec.FailoverMode,
			"targetCluster", failover.Spec.TargetCluster)

		if err := r.WorkflowAdapter.ProcessFailover(ctx, failover, failoverGroup); err != nil {
			groupLog.Error(err, "Failover workflow failed")

			// Update status for this group
			failover.Status.FailoverGroups[i].Status = "FAILED"
			failover.Status.FailoverGroups[i].Message = "Failover failed: " + err.Error()
			if updateErr := r.Status().Update(ctx, failover); updateErr != nil {
				logger.Error(updateErr, "Failed to update failover group status")
			}

			return reconcile.Result{}, err
		}

		// Update status for this group
		failover.Status.FailoverGroups[i].Status = "SUCCESS"
		if err := r.Status().Update(ctx, failover); err != nil {
			groupLog.Error(err, "Failed to update group status to SUCCESS")
			return reconcile.Result{}, err
		}

		groupLog.Info("Successfully processed failover for group")
	}

	// Overall status is SUCCESS if all groups succeeded
	allSucceeded := true
	for _, groupStatus := range failover.Status.FailoverGroups {
		if groupStatus.Status != "SUCCESS" {
			allSucceeded = false
			break
		}
	}

	if allSucceeded {
		failover.Status.Status = "SUCCESS"
		if err := r.Status().Update(ctx, failover); err != nil {
			logger.Error(err, "Failed to update failover status to SUCCESS")
			return reconcile.Result{}, err
		}
		logger.Info("Failover completed successfully for all groups")
	}

	return reconcile.Result{}, nil
}

// handleDeletion cleans up resources and removes finalizer
func (r *FailoverReconciler) handleDeletion(ctx context.Context, failover *crdv1alpha1.Failover) (reconcile.Result, error) {
	logger := log.FromContext(ctx).WithValues(
		"namespace", failover.Namespace,
		"name", failover.Name,
	)

	// Check if the finalizer is still present
	if controllerutil.ContainsFinalizer(failover, finalizerName) {
		logger.Info("Performing cleanup before deletion")

		// Clean up any DynamoDB locks or resources
		if r.DynamoDBManager != nil {
			for _, group := range failover.Spec.FailoverGroups {
				namespace := group.Namespace
				if namespace == "" {
					namespace = failover.Namespace
				}

				// Release any existing locks
				operatorID := "failover-operator" // Default

				// Try to get the failover group to check its operatorID
				failoverGroup := &crdv1alpha1.FailoverGroup{}
				err := r.Get(ctx, client.ObjectKey{
					Namespace: namespace,
					Name:      group.Name,
				}, failoverGroup)

				if err == nil && failoverGroup.Spec.OperatorID != "" {
					operatorID = failoverGroup.Spec.OperatorID
				}

				// Try to release any locks
				r.DynamoDBManager.ReleaseLock(ctx, operatorID, namespace, group.Name)
			}
		}

		controllerutil.RemoveFinalizer(failover, finalizerName)
		if err := r.Update(ctx, failover); err != nil {
			logger.Error(err, "Failed to remove finalizer")
			return reconcile.Result{}, err
		}
	}

	return reconcile.Result{}, nil
}
