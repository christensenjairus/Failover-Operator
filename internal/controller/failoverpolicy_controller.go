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

	// If error exists, requeue
	if failoverError {
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
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

	// Extract `spec.replicationState`
	specReplicationState := string(volumeReplication.Spec.ReplicationState)

	// Extract `status.state`
	statusReplicationState := strings.ToLower(string(volumeReplication.Status.State))

	// Determine desired state
	desiredReplicationState := "secondary"
	if state == "primary" {
		desiredReplicationState = "primary"
	}

	log.Info("Checking VolumeReplication",
		"name", name,
		"spec.replicationState", specReplicationState,
		"status.state", statusReplicationState,
		"desiredReplicationState", desiredReplicationState)

	if statusReplicationState == "resync" {
		log.Info("VolumeReplication is in resync mode; waiting for it to complete before applying changes", "name", name)
		return true, nil // **Still pending**
	}

	if strings.EqualFold(statusReplicationState, desiredReplicationState) {
		log.Info("VolumeReplication already in desired state, marking as complete", "name", name)
		return false, nil
	}

	if desiredReplicationState == "primary" && statusReplicationState != "secondary" {
		log.Info("Waiting for VolumeReplication to reach 'secondary' before switching to 'primary'", "name", name)
		return true, nil // **Still pending**
	}

	log.Info("Updating VolumeReplication spec", "name", name, "newState", desiredReplicationState)

	// Update the replication state
	volumeReplication.Spec.ReplicationState = replicationv1alpha1.ReplicationState(desiredReplicationState)

	// Attempt update
	if err := r.Update(ctx, volumeReplication); err != nil {
		log.Error(err, "Failed to update VolumeReplication", "VolumeReplication", name)
		return true, err
	}

	log.Info("Successfully updated VolumeReplication", "name", name, "newState", desiredReplicationState)

	return !strings.EqualFold(statusReplicationState, desiredReplicationState), nil
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

		// Ensure the state comparison is case-insensitive
		currentStateLower := strings.ToLower(currentState)
		desiredStateLower := strings.ToLower(desiredState)

		// If the current state already matches the desired state, no update is needed
		if currentStateLower == desiredStateLower {
			log.Info("VolumeReplication is already in the desired state", "VolumeReplication", vrName, "State", currentState)
			continue
		}

		// Safe mode logic: Ensure all VolumeReplications are in "secondary" before switching to "primary"
		if mode == "safe" && desiredStateLower == "primary" && currentStateLower != "secondary" {
			log.Info("Safe mode enforced: Waiting for all VolumeReplications to reach secondary before promoting to primary", "VolumeReplication", vrName)
			pendingUpdates++
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

		// Ensure FailoverInProgress condition is removed when failover is complete
		meta.RemoveStatusCondition(&failoverPolicy.Status.Conditions, "FailoverInProgress")
	} else {
		meta.SetStatusCondition(&failoverPolicy.Status.Conditions, metav1.Condition{
			Type:    "FailoverInProgress",
			Status:  metav1.ConditionTrue,
			Reason:  "SyncingReplications",
			Message: fmt.Sprintf("%d VolumeReplication objects are still syncing.", pendingUpdates),
		})

		// Ensure FailoverComplete condition is removed when failover is in progress
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
		WithOptions(controller.Options{MaxConcurrentReconciles: 2}).
		Complete(r)
}
