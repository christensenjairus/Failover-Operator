package failover

import (
	"context"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	crdv1alpha1 "github.com/christensenjairus/Failover-Operator/api/v1alpha1"
)

// FailoverReconciler reconciles a Failover object
type FailoverReconciler struct {
	client.Client
	logr.Logger
	Scheme *runtime.Scheme

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
		r.FailoverManager = NewManager(r.Client, "cluster-name", r.Logger)
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
	// Implementation of Reconcile method
	return reconcile.Result{}, nil
}
