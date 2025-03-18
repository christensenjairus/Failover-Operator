package controller

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	crdv1alpha1 "github.com/christensenjairus/Failover-Operator/api/v1alpha1"
	"github.com/christensenjairus/Failover-Operator/internal/controller/status"
	"github.com/christensenjairus/Failover-Operator/internal/controller/virtualservice"
	"github.com/christensenjairus/Failover-Operator/internal/controller/volumereplication"
	replicationv1alpha1 "github.com/csi-addons/kubernetes-csi-addons/api/replication.storage/v1alpha1"
)

// Deprecated: Use RegisterSchemes from setup.go instead
func init() {
	// This function is kept for backwards compatibility
	// Please use RegisterSchemes from setup.go instead
	replicationv1alpha1.SchemeBuilder.Register(&replicationv1alpha1.VolumeReplication{}, &replicationv1alpha1.VolumeReplicationList{})
}

// FailoverPolicyReconciler reconciles a FailoverPolicy object
type FailoverPolicyReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=crd.hahomelabs.com,resources=failoverpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=crd.hahomelabs.com,resources=failoverpolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=crd.hahomelabs.com,resources=failoverpolicies/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=apps,resources=virtualservices,verbs=get;list;watch;update;patch

func (r *FailoverPolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Fetch FailoverPolicy object
	failoverPolicy := &crdv1alpha1.FailoverPolicy{}
	if err := r.Get(ctx, req.NamespacedName, failoverPolicy); err != nil {
		log.Error(err, "Failed to get FailoverPolicy")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	log.Info("Reconciling FailoverPolicy", "FailoverPolicy", failoverPolicy.Name)

	// Initialize our managers
	vrManager := volumereplication.NewManager(r.Client)
	vsManager := virtualservice.NewManager(r.Client)
	statusManager := status.NewManager(r.Client, vrManager)

	// Process VolumeReplication failover
	pendingUpdates, failoverError, failoverErrorMessage := vrManager.ProcessVolumeReplications(
		ctx,
		failoverPolicy.Namespace,
		failoverPolicy.Spec.VolumeReplications,
		failoverPolicy.Spec.DesiredState,
		failoverPolicy.Spec.Mode,
	)

	// Process VirtualService updates
	vsManager.ProcessVirtualServices(
		ctx,
		failoverPolicy.Namespace,
		failoverPolicy.Spec.VirtualServices,
		failoverPolicy.Spec.DesiredState,
	)

	// Update FailoverPolicy status
	if err := statusManager.UpdateFailoverStatus(ctx, failoverPolicy, pendingUpdates, failoverError, failoverErrorMessage); err != nil {
		return ctrl.Result{}, err
	}

	// Only requeue if there are pending updates or errors
	if pendingUpdates > 0 || failoverError {
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	return ctrl.Result{}, nil
}

func (r *FailoverPolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := RegisterSchemes(mgr.GetScheme()); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&crdv1alpha1.FailoverPolicy{}).
		Owns(&replicationv1alpha1.VolumeReplication{}).
		Watches(
			&replicationv1alpha1.VolumeReplication{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
				// Find all FailoverPolicies in the same namespace
				failoverPolicies := &crdv1alpha1.FailoverPolicyList{}
				if err := r.List(context.Background(), failoverPolicies, client.InNamespace(obj.GetNamespace())); err != nil {
					return nil
				}

				var requests []reconcile.Request
				for _, policy := range failoverPolicies.Items {
					// Check if this VolumeReplication is referenced in the policy
					for _, vrName := range policy.Spec.VolumeReplications {
						if vrName == obj.GetName() {
							requests = append(requests, reconcile.Request{
								NamespacedName: types.NamespacedName{
									Name:      policy.Name,
									Namespace: policy.Namespace,
								},
							})
							break
						}
					}
				}
				return requests
			}),
		).
		WithOptions(controller.Options{MaxConcurrentReconciles: 2}).
		Complete(r)
}
