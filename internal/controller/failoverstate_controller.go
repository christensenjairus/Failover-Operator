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

	failoverv1alpha1 "github.com/christensenjairus/Failover-Operator/api/v1alpha1"
)

func init() {
	replicationv1alpha1.SchemeBuilder.Register(&replicationv1alpha1.VolumeReplication{}, &replicationv1alpha1.VolumeReplicationList{})
}

// FailoverStateReconciler reconciles a FailoverState object
type FailoverStateReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=failover.cyber-engine.com,resources=failoverstates,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=failover.cyber-engine.com,resources=failoverstates/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=failover.cyber-engine.com,resources=failoverstates/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=apps,resources=virtualservices,verbs=get;list;watch;update;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the FailoverState object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/reconcile

func (r *FailoverStateReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Fetch the FailoverState instance
	failoverState := &failoverv1alpha1.FailoverState{}
	err := r.Get(ctx, req.NamespacedName, failoverState)
	if err != nil {
		log.Error(err, "Failed to get FailoverState")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	log.Info("Reconciling FailoverState", "FailoverState", failoverState.Name)

	desiredState := failoverState.Spec.FailoverState // "primary" or "secondary"
	var pendingVolumeReplicationUpdates int
	var failoverErrorMessage string
	var hasError bool // Track if any VolumeReplication has failed

	// ✅ Check all VolumeReplication objects
	for _, vrName := range failoverState.Spec.VolumeReplications {
		log.Info("Checking VolumeReplication", "VolumeReplication", vrName)

		// ✅ Detect errors in VolumeReplication
		errorMessage, errorDetected := r.checkVolumeReplicationError(ctx, vrName, failoverState.Namespace)
		if errorDetected {
			failoverErrorMessage = fmt.Sprintf("VolumeReplication %s: %s", vrName, errorMessage)
			hasError = true
			continue // **Skip further processing, do NOT count this as pending**
		}

		// ✅ Only count VolumeReplications that are actually pending an update
		pending, err := r.updateVolumeReplication(ctx, vrName, failoverState.Namespace, desiredState)
		if err != nil {
			log.Error(err, "Failed to update VolumeReplication", "VolumeReplication", vrName)
			return ctrl.Result{}, err
		}
		if pending {
			pendingVolumeReplicationUpdates++
		}
	}

	// ✅ If error was detected, mark FailoverState as "Error" **with VolumeReplication name**
	if hasError {
		meta.SetStatusCondition(&failoverState.Status.Conditions, metav1.Condition{
			Type:               "FailoverError",
			Status:             metav1.ConditionTrue,
			Reason:             "VolumeReplicationError",
			Message:            failoverErrorMessage, // ✅ Now includes which VolumeReplication failed
			LastTransitionTime: metav1.Now(),
		})

		// ✅ Remove "FailoverComplete" and "FailoverInProgress" conditions
		meta.RemoveStatusCondition(&failoverState.Status.Conditions, "FailoverComplete")
		meta.RemoveStatusCondition(&failoverState.Status.Conditions, "FailoverInProgress")

		// ✅ Update status
		failoverState.Status.PendingVolumeReplicationUpdates = pendingVolumeReplicationUpdates
		if err := r.Status().Update(ctx, failoverState); err != nil {
			log.Error(err, "Failed to update FailoverState status")
			return ctrl.Result{}, err
		}

		log.Error(fmt.Errorf("failover error"), "FailoverState entered an error state", "FailoverState", failoverState.Name, "Error", failoverErrorMessage)
	}

	// ✅ **Fix: Ensure VirtualServices are updated, even if there was an error**
	for _, vsName := range failoverState.Spec.VirtualServices {
		log.Info("Checking VirtualService update", "VirtualService", vsName, "desiredState", desiredState) // 🔥 DEBUGGING LOG

		updated, err := r.updateVirtualService(ctx, vsName, failoverState.Namespace, desiredState)
		if err != nil {
			log.Error(err, "Failed to update VirtualService", "VirtualService", vsName)
			return ctrl.Result{}, err
		}
		if updated {
			log.Info("Updated VirtualService", "VirtualService", vsName)
		}
	}

	// ✅ If an error exists, return **after** updating VirtualServices
	if hasError {
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// ✅ Remove FailoverError condition if no errors
	meta.RemoveStatusCondition(&failoverState.Status.Conditions, "FailoverError")

	// ✅ Normal Failover Process
	failoverComplete := (pendingVolumeReplicationUpdates == 0)
	failoverState.Status.PendingVolumeReplicationUpdates = pendingVolumeReplicationUpdates

	if failoverComplete {
		// ✅ Set "FailoverComplete"
		failoverState.Status.AppliedState = desiredState
		meta.SetStatusCondition(&failoverState.Status.Conditions, metav1.Condition{
			Type:               "FailoverComplete",
			Status:             metav1.ConditionTrue,
			Reason:             "AllReplicationsSynced",
			Message:            "All VolumeReplication objects have been updated successfully.",
			LastTransitionTime: metav1.Now(),
		})
	} else {
		// ✅ Set "FailoverInProgress"
		meta.SetStatusCondition(&failoverState.Status.Conditions, metav1.Condition{
			Type:               "FailoverInProgress",
			Status:             metav1.ConditionTrue,
			Reason:             "SyncingReplications",
			Message:            fmt.Sprintf("%d VolumeReplication objects are still syncing.", pendingVolumeReplicationUpdates),
			LastTransitionTime: metav1.Now(),
		})
	}

	// ✅ Remove "FailoverComplete" if still in progress
	if !failoverComplete {
		meta.RemoveStatusCondition(&failoverState.Status.Conditions, "FailoverComplete")
	}

	// ✅ Update status in Kubernetes
	if err := r.Status().Update(ctx, failoverState); err != nil {
		log.Error(err, "Failed to update FailoverState status")
		return ctrl.Result{}, err
	}

	log.Info("Successfully reconciled FailoverState", "FailoverState", failoverState.Name)
	return ctrl.Result{}, nil
}

func (r *FailoverStateReconciler) updateVolumeReplication(ctx context.Context, name, namespace, state string) (bool, error) {
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

	// ✅ **Wait if resync is in progress**
	if statusReplicationState == "resync" {
		log.Info("VolumeReplication is in resync mode; waiting for it to complete before applying changes", "name", name)
		return true, nil // **Still pending**
	}

	// ✅ **If already in the correct state, no update needed**
	if strings.EqualFold(statusReplicationState, desiredReplicationState) {
		log.Info("VolumeReplication already in desired state, marking as complete", "name", name)
		return false, nil
	}

	// ✅ **If transitioning to primary, wait until secondary first**
	if desiredReplicationState == "primary" && statusReplicationState != "secondary" {
		log.Info("Waiting for VolumeReplication to reach 'secondary' before switching to 'primary'", "name", name)
		return true, nil // **Still pending**
	}

	// ✅ **Update needed**
	log.Info("Updating VolumeReplication spec", "name", name, "newState", desiredReplicationState)

	// Update the replication state
	volumeReplication.Spec.ReplicationState = replicationv1alpha1.ReplicationState(desiredReplicationState)

	// Attempt update
	if err := r.Update(ctx, volumeReplication); err != nil {
		log.Error(err, "Failed to update VolumeReplication", "VolumeReplication", name)
		return true, err
	}

	log.Info("Successfully updated VolumeReplication", "name", name, "newState", desiredReplicationState)

	// ✅ **Mark as pending if status still doesn’t match**
	return !strings.EqualFold(statusReplicationState, desiredReplicationState), nil
}

func (r *FailoverStateReconciler) checkVolumeReplicationError(ctx context.Context, name, namespace string) (string, bool) {
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

// updateVirtualService ensures VirtualService annotation matches the desired state
func (r *FailoverStateReconciler) updateVirtualService(ctx context.Context, name, namespace, state string) (bool, error) {
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

// SetupWithManager sets up the controller with the Manager.
func (r *FailoverStateReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := replicationv1alpha1.AddToScheme(mgr.GetScheme()); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&failoverv1alpha1.FailoverState{}).
		Owns(&replicationv1alpha1.VolumeReplication{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: 2}).
		Complete(r)
}
