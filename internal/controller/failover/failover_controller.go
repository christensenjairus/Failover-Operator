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

	// Process the failover using the manager
	err := r.FailoverManager.ProcessFailover(ctx, failover)
	if err != nil {
		logger.Error(err, "Failed to process failover")
		// Update status to reflect the error
		failover.Status.Status = "FAILED"
		// Add error to conditions instead of using Message field
		if updateErr := r.Status().Update(ctx, failover); updateErr != nil {
			logger.Error(updateErr, "Failed to update Failover status")
		}
		return reconcile.Result{}, err
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

		// Let the manager handle cleanup operations if needed
		// For now, just remove the finalizer without cleanup
		controllerutil.RemoveFinalizer(failover, finalizerName)
		if err := r.Update(ctx, failover); err != nil {
			logger.Error(err, "Failed to remove finalizer")
			return reconcile.Result{}, err
		}
	}

	return reconcile.Result{}, nil
}
