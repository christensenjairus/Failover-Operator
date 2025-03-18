package controller

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
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
	"github.com/christensenjairus/Failover-Operator/internal/controller/volumereplication"
	"github.com/christensenjairus/Failover-Operator/internal/controller/workload"
	replicationv1alpha1 "github.com/csi-addons/kubernetes-csi-addons/api/replication.storage/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
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
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=batch,resources=cronjobs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=apps,resources=virtualservices,verbs=get;list;watch;update;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *FailoverPolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Fetch the FailoverPolicy instance
	failoverPolicy := &crdv1alpha1.FailoverPolicy{}
	err := r.Get(ctx, req.NamespacedName, failoverPolicy)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found - could have been deleted after reconcile request.
			// Return and don't requeue
			log.Info("FailoverPolicy resource not found. Ignoring since object must be deleted.")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		log.Error(err, "Failed to get FailoverPolicy")
		return ctrl.Result{}, err
	}

	// Create managers
	vrManager := volumereplication.NewManager(r.Client)
	workloadManager := workload.NewManager(r.Client)
	statusManager := status.NewManager(r.Client, vrManager)

	// Handle VolumeReplications
	pendingUpdates, failoverError, failoverErrorMessage := vrManager.ProcessVolumeReplications(ctx, failoverPolicy.Namespace, failoverPolicy.Spec.VolumeReplications, failoverPolicy.Spec.DesiredState, failoverPolicy.Spec.Mode)
	if failoverError {
		log.Error(nil, "Failed to process volume replications", "error", failoverErrorMessage)
		statusErr := statusManager.UpdateFailoverStatus(ctx, failoverPolicy, failoverError, failoverErrorMessage)
		if statusErr != nil {
			log.Error(statusErr, "Failed to update status after volume replication error")
		}
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	// Process workloads based on the mode
	if err := workloadManager.ProcessWorkloads(ctx, failoverPolicy.Namespace,
		failoverPolicy.Spec.Deployments,
		failoverPolicy.Spec.StatefulSets,
		failoverPolicy.Spec.CronJobs,
		failoverPolicy.Spec.DesiredState); err != nil {
		log.Error(err, "Failed to process workloads")
		statusErr := statusManager.UpdateFailoverStatus(ctx, failoverPolicy, true, fmt.Sprintf("Failed to process workloads: %v", err))
		if statusErr != nil {
			log.Error(statusErr, "Failed to update status after workload error")
		}
		return ctrl.Result{}, err
	}

	// Update status
	if err := statusManager.UpdateStatus(ctx, failoverPolicy); err != nil {
		log.Error(err, "Failed to update status")
		return ctrl.Result{}, err
	}

	// If there are pending volume replication updates, requeue sooner
	if pendingUpdates > 0 {
		log.Info("Volume replication updates pending, requeueing", "pendingUpdates", pendingUpdates)
		failoverPolicy.Status.PendingVolumeReplicationUpdates = pendingUpdates

		if err := r.Status().Update(ctx, failoverPolicy); err != nil {
			log.Error(err, "Failed to update pending updates count in status")
			return ctrl.Result{}, err
		}

		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	// Update status to indicate successful reconciliation
	statusErr := statusManager.UpdateFailoverStatus(ctx, failoverPolicy, false, "")
	if statusErr != nil {
		log.Error(statusErr, "Failed to update status after successful reconciliation")
		return ctrl.Result{}, statusErr
	}

	// If in secondary mode, requeue periodically to ensure workloads stay scaled down
	if failoverPolicy.Spec.DesiredState == "secondary" {
		// Check if any workload is not in the desired state
		needsRequeueSoon := false
		if len(failoverPolicy.Status.WorkloadStatuses) > 0 {
			for _, status := range failoverPolicy.Status.WorkloadStatuses {
				if (status.Kind == "Deployment" || status.Kind == "StatefulSet") && status.State != "Scaled Down" {
					needsRequeueSoon = true
					break
				} else if status.Kind == "CronJob" && status.State != "Suspended" {
					needsRequeueSoon = true
					break
				}
			}
		}

		requeueAfter := 20 * time.Second
		if needsRequeueSoon {
			requeueAfter = 5 * time.Second
			log.Info("Detected workloads not in proper state, requeueing soon", "RequeueAfter", requeueAfter)
		}

		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}

	log.Info("Reconciliation completed",
		"Name", failoverPolicy.Name,
		"Namespace", failoverPolicy.Namespace,
		"DesiredState", failoverPolicy.Spec.DesiredState)
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *FailoverPolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := RegisterSchemes(mgr.GetScheme()); err != nil {
		return err
	}

	// Create a watch handler function for workload resources
	workloadMapFunc := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		// Find all FailoverPolicies in the same namespace
		failoverPolicies := &crdv1alpha1.FailoverPolicyList{}
		if err := r.List(context.Background(), failoverPolicies, client.InNamespace(obj.GetNamespace())); err != nil {
			return nil
		}

		var requests []reconcile.Request
		objName := obj.GetName()
		objKind := obj.GetObjectKind().GroupVersionKind().Kind

		for _, policy := range failoverPolicies.Items {
			shouldEnqueue := false

			// Check if this resource is referenced in the policy
			switch objKind {
			case "Deployment":
				for _, name := range policy.Spec.Deployments {
					if name == objName {
						shouldEnqueue = true
						break
					}
				}
			case "StatefulSet":
				for _, name := range policy.Spec.StatefulSets {
					if name == objName {
						shouldEnqueue = true
						break
					}
				}
			case "CronJob":
				for _, name := range policy.Spec.CronJobs {
					if name == objName {
						shouldEnqueue = true
						break
					}
				}
			}

			if shouldEnqueue {
				requests = append(requests, reconcile.Request{
					NamespacedName: types.NamespacedName{
						Name:      policy.Name,
						Namespace: policy.Namespace,
					},
				})
			}
		}
		return requests
	})

	// Create a watch handler function for VolumeReplication resources
	vrMapFunc := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
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
	})

	return ctrl.NewControllerManagedBy(mgr).
		For(&crdv1alpha1.FailoverPolicy{}).
		Owns(&replicationv1alpha1.VolumeReplication{}).
		Watches(
			&replicationv1alpha1.VolumeReplication{},
			vrMapFunc,
		).
		Watches(
			&appsv1.Deployment{},
			workloadMapFunc,
		).
		Watches(
			&appsv1.StatefulSet{},
			workloadMapFunc,
		).
		Watches(
			&batchv1.CronJob{},
			workloadMapFunc,
		).
		WithOptions(controller.Options{MaxConcurrentReconciles: 2}).
		Complete(r)
}
