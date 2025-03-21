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
)

const finalizerName = "failover.failover-operator.io/finalizer"

// FailoverReconciler reconciles a Failover object
type FailoverReconciler struct {
	client.Client
	logr.Logger
	Scheme      *runtime.Scheme
	ClusterName string

	FailoverManager *Manager
	cancelFunc      context.CancelFunc
}

// SetupWithManager sets up the controller with the Manager.
func (r *FailoverReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Create a context for the automatic failover checker
	ctx, cancel := context.WithCancel(context.Background())
	r.cancelFunc = cancel

	// Initialize the FailoverManager if not already initialized
	if r.FailoverManager == nil {
		r.FailoverManager = NewManager(r.Client, r.ClusterName, r.Logger)
	}

	// Start the automatic failover checker
	r.FailoverManager.StartAutomaticFailoverChecker(ctx, 30) // Check every 30 seconds

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

	// Process the failover using the manager
	// This will execute the staged failover process:
	// Stage 1: Initialization
	// Stage 2: Source Cluster Preparation
	// Stage 3: Volume Demotion (Source Cluster)
	// Stage 4: Volume Promotion (Target Cluster)
	// Stage 5: Target Cluster Activation
	// Stage 6: Completion
	// See failover_stages.go for detailed documentation of each stage
	logger.Info("Processing failover",
		"targetCluster", failover.Spec.TargetCluster,
		"groups", len(failover.Spec.FailoverGroups))

	err := r.FailoverManager.ProcessFailover(ctx, failover)
	if err != nil {
		// Handle error according to our error handling guidelines
		// See "Error Handling (Any Stage)" in failover_stages.go
		logger.Error(err, "Failed to process failover")

		// Update status to reflect the error
		failover.Status.Status = "FAILED"

		// Add error to conditions with detailed information
		if updateErr := r.Status().Update(ctx, failover); updateErr != nil {
			logger.Error(updateErr, "Failed to update Failover status after error")
			// Return the original error, not the status update error
		}

		// Log additional diagnostic information for complex errors
		r.logFailoverDiagnostics(ctx, failover, err)

		return reconcile.Result{}, err
	}

	logger.Info("Failover processed successfully")
	return reconcile.Result{}, nil
}

// logFailoverDiagnostics logs additional diagnostic information for failed failovers
func (r *FailoverReconciler) logFailoverDiagnostics(ctx context.Context, failover *crdv1alpha1.Failover, err error) {
	logger := log.FromContext(ctx).WithValues(
		"namespace", failover.Namespace,
		"name", failover.Name,
	)

	// Add extra diagnostic info based on error type and stage
	// This helps administrators troubleshoot failures
	logger.Info("Failover diagnostic information",
		"error", err.Error(),
		"targetCluster", failover.Spec.TargetCluster,
		"failoverMode", failover.Spec.FailoverMode,
		"groups", len(failover.Spec.FailoverGroups))

	// Depending on the specific error types, we might collect and log
	// additional information about cluster state, resources, etc.
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

		// Clean up any lingering resources from incomplete failovers
		if err := r.FailoverManager.CleanupFailoverResources(ctx, failover); err != nil {
			logger.Error(err, "Failed to clean up failover resources")
			// Continue with deletion even if cleanup fails
		}

		controllerutil.RemoveFinalizer(failover, finalizerName)
		if err := r.Update(ctx, failover); err != nil {
			logger.Error(err, "Failed to remove finalizer")
			return reconcile.Result{}, err
		}
	}

	return reconcile.Result{}, nil
}
