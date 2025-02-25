package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/log"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	replicationv1alpha1 "github.com/csi-addons/kubernetes-csi-addons/api/replication.storage/v1alpha1"

	crdv1alpha1 "github.com/christensenjairus/Failover-Operator/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func init() {
	replicationv1alpha1.SchemeBuilder.Register(&replicationv1alpha1.VolumeReplication{}, &replicationv1alpha1.VolumeReplicationList{})
}

// FailoverPolicyReconciler reconciles a FailoverPolicy object
type FailoverPolicyReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=crd.cyber-engine.com,resources=failoverpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=crd.cyber-engine.com,resources=failoverpolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=crd.cyber-engine.com,resources=failoverpolicies/finalizers,verbs=update
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

	// Process VolumeReplication failover
	pendingUpdates, failoverError, failoverErrorMessage := r.processVolumeReplications(ctx, failoverPolicy)

	// Process VirtualService updates
	r.processVirtualServices(ctx, failoverPolicy)

	// Update FailoverPolicy status
	if err := r.updateFailoverStatus(ctx, failoverPolicy, pendingUpdates, failoverError, failoverErrorMessage); err != nil {
		return ctrl.Result{}, err
	}

	// Only requeue if there are pending updates or errors
	if pendingUpdates > 0 || failoverError {
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	return ctrl.Result{}, nil
}

func (r *FailoverPolicyReconciler) updateVolumeReplication(ctx context.Context, name, namespace, state string) (bool, error) {
	log := log.FromContext(ctx)
	volumeReplication := &replicationv1alpha1.VolumeReplication{}

	// Fetch the VolumeReplication object
	err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, volumeReplication)
	if err != nil {
		log.Error(err, "Failed to get VolumeReplication", "VolumeReplication", name)
		return false, client.IgnoreNotFound(err)
	}

	currentSpec := string(volumeReplication.Spec.ReplicationState)
	currentStatus := strings.ToLower(string(volumeReplication.Status.State))
	desiredState := strings.ToLower(state)

	log.Info("Checking VolumeReplication state",
		"name", name,
		"currentSpec", currentSpec,
		"currentStatus", currentStatus,
		"desiredState", desiredState)

	// If in resync mode, wait for completion
	if currentStatus == "resync" {
		log.Info("VolumeReplication is in resync mode - waiting for completion",
			"name", name)
		return true, nil
	}

	// If transitioning to primary, ensure current status is either secondary or already primary
	if desiredState == "primary" && currentStatus != "secondary" && currentStatus != "primary" {
		log.Info("Cannot transition to primary - current status must be secondary or primary",
			"name", name,
			"currentStatus", currentStatus)
		return true, nil
	}

	// Only update spec if it doesn't match desired state and status is stable
	if currentSpec != state {
		// Check if status is in a stable state before updating
		if currentStatus == "error" {
			log.Info("Cannot update spec - VolumeReplication is in error state",
				"name", name)
			return true, nil
		}

		log.Info("Updating VolumeReplication spec",
			"name", name,
			"currentSpec", currentSpec,
			"desiredState", state)

		volumeReplication.Spec.ReplicationState = replicationv1alpha1.ReplicationState(state)
		if err := r.Update(ctx, volumeReplication); err != nil {
			log.Error(err, "Failed to update VolumeReplication", "VolumeReplication", name)
			return true, err
		}
		return true, nil
	}

	// Check if status matches spec
	if currentStatus != desiredState {
		log.Info("VolumeReplication status not yet matching spec",
			"name", name,
			"statusState", currentStatus,
			"desiredState", desiredState)
		return true, nil
	}

	return false, nil
}

func (r *FailoverPolicyReconciler) checkVolumeReplicationError(ctx context.Context, name, namespace string) (string, bool) {
	log := log.FromContext(ctx)
	volumeReplication := &replicationv1alpha1.VolumeReplication{}

	// Fetch the VolumeReplication object
	err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, volumeReplication)
	if err != nil {
		log.Error(err, "Failed to get VolumeReplication", "VolumeReplication", name)
		return "", false
	}

	// Scan conditions for errors
	for _, condition := range volumeReplication.Status.Conditions {
		if condition.Type == "Degraded" && condition.Status == metav1.ConditionTrue {
			log.Error(fmt.Errorf("VolumeReplication degraded"), "VolumeReplication has an error", "name", name, "reason", condition.Reason, "message", condition.Message)
			return condition.Message, true
		}
	}

	return "", false // No errors found
}

func (r *FailoverPolicyReconciler) getCurrentVolumeReplicationState(ctx context.Context, name, namespace string) (string, error) {
	volumeReplication := &replicationv1alpha1.VolumeReplication{}

	err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, volumeReplication)
	if err != nil {
		return "", err
	}

	// Convert to lowercase to ensure consistent comparison
	return strings.ToLower(string(volumeReplication.Status.State)), nil
}

func (r *FailoverPolicyReconciler) updateVirtualService(ctx context.Context, name, namespace, state string) (bool, error) {
	virtualService := &unstructured.Unstructured{}
	virtualService.SetAPIVersion("networking.istio.io/v1")
	virtualService.SetKind("VirtualService")

	// Fetch VirtualService object
	err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, virtualService)
	if err != nil {
		return false, client.IgnoreNotFound(err)
	}

	// Ensure annotations exist
	annotations := virtualService.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}

	// Determine the correct annotation based on failover state
	desiredAnnotation := "ignore"
	if state == "primary" { // <- FIXED: Ensures lowercase comparison
		desiredAnnotation = "dns-controller"
	}

	// Check if update is needed
	currentAnnotation, found := annotations["external-dns.alpha.kubernetes.io/controller"]
	if !found || currentAnnotation != desiredAnnotation {
		annotations["external-dns.alpha.kubernetes.io/controller"] = desiredAnnotation
		virtualService.SetAnnotations(annotations)

		if err := r.Update(ctx, virtualService); err != nil {
			return false, err
		}
		return true, nil // Indicates an update was made
	}

	return false, nil // No update needed
}

func (r *FailoverPolicyReconciler) processVolumeReplications(ctx context.Context, failoverPolicy *crdv1alpha1.FailoverPolicy) (int, bool, string) {
	log := log.FromContext(ctx)
	desiredState := failoverPolicy.Spec.DesiredState
	mode := failoverPolicy.Spec.Mode

	var pendingUpdates int
	var failoverError bool
	var failoverErrorMessage string

	// For safe mode, first check if all VolumeReplications are ready for primary transition
	if mode == "safe" && strings.ToLower(desiredState) == "primary" {
		allReady := true
		for _, vrName := range failoverPolicy.Spec.VolumeReplications {
			currentState, err := r.getCurrentVolumeReplicationState(ctx, vrName, failoverPolicy.Namespace)
			if err != nil {
				failoverErrorMessage = fmt.Sprintf("Failed to retrieve state for VolumeReplication %s", vrName)
				return 0, true, failoverErrorMessage
			}

			// In safe mode, volumes must be either already primary or secondary before transitioning
			if currentState != "primary" && currentState != "secondary" {
				log.Info("Safe mode: waiting for VolumeReplication to be in valid state",
					"VolumeReplication", vrName,
					"CurrentState", currentState,
					"ValidStates", []string{"primary", "secondary"})
				allReady = false
				pendingUpdates++
			}
		}

		// Only return early if not all volumes are ready
		if !allReady {
			return pendingUpdates, false, ""
		}
	}

	// Process all VolumeReplications
	for _, vrName := range failoverPolicy.Spec.VolumeReplications {
		log.Info("Checking VolumeReplication", "VolumeReplication", vrName)

		// Detect VolumeReplication errors
		errorMessage, errorDetected := r.checkVolumeReplicationError(ctx, vrName, failoverPolicy.Namespace)
		if errorDetected {
			failoverErrorMessage = fmt.Sprintf("VolumeReplication %s: %s", vrName, errorMessage)
			failoverError = true
			continue
		}

		// Get current replication state
		currentState, err := r.getCurrentVolumeReplicationState(ctx, vrName, failoverPolicy.Namespace)
		if err != nil {
			failoverErrorMessage = fmt.Sprintf("Failed to retrieve state for VolumeReplication %s", vrName)
			failoverError = true
			continue
		}

		// If the current state already matches the desired state, no update is needed
		if strings.EqualFold(currentState, desiredState) {
			log.Info("VolumeReplication is already in the desired state",
				"VolumeReplication", vrName,
				"State", currentState)
			continue
		}

		// Attempt to update VolumeReplication
		pending, err := r.updateVolumeReplication(ctx, vrName, failoverPolicy.Namespace, desiredState)
		if err != nil {
			log.Error(err, "Failed to update VolumeReplication", "VolumeReplication", vrName)
			return 0, true, "Failed to update VolumeReplication"
		}
		if pending {
			pendingUpdates++
		}
	}

	return pendingUpdates, failoverError, failoverErrorMessage
}

func (r *FailoverPolicyReconciler) processVirtualServices(ctx context.Context, failoverPolicy *crdv1alpha1.FailoverPolicy) {
	log := log.FromContext(ctx)
	desiredState := failoverPolicy.Spec.DesiredState

	for _, vsName := range failoverPolicy.Spec.VirtualServices {
		log.Info("Checking VirtualService update", "VirtualService", vsName)

		updated, err := r.updateVirtualService(ctx, vsName, failoverPolicy.Namespace, desiredState)
		if err != nil {
			log.Error(err, "Failed to update VirtualService", "VirtualService", vsName)
			continue
		}
		if updated {
			log.Info("Updated VirtualService", "VirtualService", vsName)
		}
	}
}

func (r *FailoverPolicyReconciler) updateFailoverStatus(ctx context.Context, failoverPolicy *crdv1alpha1.FailoverPolicy, pendingUpdates int, failoverError bool, failoverErrorMessage string) error {
	log := log.FromContext(ctx)

	// Only track problematic VolumeReplications
	var statuses []crdv1alpha1.VolumeReplicationStatus
	for _, vrName := range failoverPolicy.Spec.VolumeReplications {
		currentState, err := r.getCurrentVolumeReplicationState(ctx, vrName, failoverPolicy.Namespace)
		if err != nil {
			// Include in status if we can't get the state
			statuses = append(statuses, crdv1alpha1.VolumeReplicationStatus{
				Name:           vrName,
				Error:          fmt.Sprintf("Failed to get state: %v", err),
				LastUpdateTime: time.Now().Format(time.RFC3339),
			})
			continue
		}

		// Check for errors
		if errorMsg, hasError := r.checkVolumeReplicationError(ctx, vrName, failoverPolicy.Namespace); hasError {
			statuses = append(statuses, crdv1alpha1.VolumeReplicationStatus{
				Name:           vrName,
				CurrentState:   currentState,
				DesiredState:   failoverPolicy.Spec.DesiredState,
				Error:          errorMsg,
				LastUpdateTime: time.Now().Format(time.RFC3339),
			})
			continue
		}

		// Only include if not in desired state
		if !strings.EqualFold(currentState, failoverPolicy.Spec.DesiredState) {
			statuses = append(statuses, crdv1alpha1.VolumeReplicationStatus{
				Name:           vrName,
				CurrentState:   currentState,
				DesiredState:   failoverPolicy.Spec.DesiredState,
				LastUpdateTime: time.Now().Format(time.RFC3339),
			})
		}
	}

	// Update status with only problematic VolumeReplications
	failoverPolicy.Status.VolumeReplicationStatuses = statuses

	if failoverError {
		meta.SetStatusCondition(&failoverPolicy.Status.Conditions, metav1.Condition{
			Type:    "FailoverError",
			Status:  metav1.ConditionTrue,
			Reason:  "VolumeReplicationError",
			Message: failoverErrorMessage,
		})

		meta.RemoveStatusCondition(&failoverPolicy.Status.Conditions, "FailoverComplete")
		meta.RemoveStatusCondition(&failoverPolicy.Status.Conditions, "FailoverInProgress")
	} else {
		meta.RemoveStatusCondition(&failoverPolicy.Status.Conditions, "FailoverError")
	}

	failoverComplete := (pendingUpdates == 0)
	failoverPolicy.Status.PendingVolumeReplicationUpdates = pendingUpdates

	if failoverComplete {
		failoverPolicy.Status.CurrentState = failoverPolicy.Spec.DesiredState
		meta.SetStatusCondition(&failoverPolicy.Status.Conditions, metav1.Condition{
			Type:    "FailoverComplete",
			Status:  metav1.ConditionTrue,
			Reason:  "AllReplicationsSynced",
			Message: "All VolumeReplication objects have been updated successfully.",
		})

		meta.RemoveStatusCondition(&failoverPolicy.Status.Conditions, "FailoverInProgress")
	} else {
		meta.SetStatusCondition(&failoverPolicy.Status.Conditions, metav1.Condition{
			Type:    "FailoverInProgress",
			Status:  metav1.ConditionTrue,
			Reason:  "SyncingReplications",
			Message: fmt.Sprintf("%d VolumeReplication objects are still syncing.", pendingUpdates),
		})

		meta.RemoveStatusCondition(&failoverPolicy.Status.Conditions, "FailoverComplete")
	}

	if err := r.Status().Update(ctx, failoverPolicy); err != nil {
		log.Error(err, "Failed to update FailoverPolicy status")
		return err
	}

	log.Info("Updated FailoverPolicy status", "FailoverPolicy", failoverPolicy.Name)
	return nil
}

func (r *FailoverPolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := replicationv1alpha1.AddToScheme(mgr.GetScheme()); err != nil {
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
