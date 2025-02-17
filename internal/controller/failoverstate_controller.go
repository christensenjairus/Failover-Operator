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

	// Fetch FailoverState object
	failoverState := &failoverv1alpha1.FailoverState{}
	if err := r.Get(ctx, req.NamespacedName, failoverState); err != nil {
		log.Error(err, "Failed to get FailoverState")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	log.Info("Reconciling FailoverState", "FailoverState", failoverState.Name)

	// Process VolumeReplication failover
	pendingUpdates, failoverError, failoverErrorMessage := r.processVolumeReplications(ctx, failoverState)

	// Process VirtualService updates
	r.processVirtualServices(ctx, failoverState)

	// Update FailoverState status
	if err := r.updateFailoverStatus(ctx, failoverState, pendingUpdates, failoverError, failoverErrorMessage); err != nil {
		return ctrl.Result{}, err
	}

	// If error exists, requeue
	if failoverError {
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

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

// processVolumeReplications manages VolumeReplication updates & error tracking
func (r *FailoverStateReconciler) processVolumeReplications(ctx context.Context, failoverState *failoverv1alpha1.FailoverState) (int, bool, string) {
	log := log.FromContext(ctx)
	desiredState := failoverState.Spec.FailoverState

	var pendingUpdates int
	var failoverError bool
	var failoverErrorMessage string

	for _, vrName := range failoverState.Spec.VolumeReplications {
		log.Info("Checking VolumeReplication", "VolumeReplication", vrName)

		// Detect VolumeReplication errors
		errorMessage, errorDetected := r.checkVolumeReplicationError(ctx, vrName, failoverState.Namespace)
		if errorDetected {
			failoverErrorMessage = fmt.Sprintf("VolumeReplication %s: %s", vrName, errorMessage)
			failoverError = true
			continue // Skip processing this VolumeReplication
		}

		// Attempt to update VolumeReplication
		pending, err := r.updateVolumeReplication(ctx, vrName, failoverState.Namespace, desiredState)
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

// processVirtualServices updates VirtualService resources
func (r *FailoverStateReconciler) processVirtualServices(ctx context.Context, failoverState *failoverv1alpha1.FailoverState) {
	log := log.FromContext(ctx)
	desiredState := failoverState.Spec.FailoverState

	for _, vsName := range failoverState.Spec.VirtualServices {
		log.Info("Checking VirtualService update", "VirtualService", vsName)

		updated, err := r.updateVirtualService(ctx, vsName, failoverState.Namespace, desiredState)
		if err != nil {
			log.Error(err, "Failed to update VirtualService", "VirtualService", vsName)
			continue
		}
		if updated {
			log.Info("Updated VirtualService", "VirtualService", vsName)
		}
	}
}

// updateFailoverStatus updates the status of FailoverState
func (r *FailoverStateReconciler) updateFailoverStatus(ctx context.Context, failoverState *failoverv1alpha1.FailoverState, pendingUpdates int, failoverError bool, failoverErrorMessage string) error {
	log := log.FromContext(ctx)

	if failoverError {
		meta.SetStatusCondition(&failoverState.Status.Conditions, metav1.Condition{
			Type:    "FailoverError",
			Status:  metav1.ConditionTrue,
			Reason:  "VolumeReplicationError",
			Message: failoverErrorMessage,
		})

		meta.RemoveStatusCondition(&failoverState.Status.Conditions, "FailoverComplete")
		meta.RemoveStatusCondition(&failoverState.Status.Conditions, "FailoverInProgress")
	} else {
		meta.RemoveStatusCondition(&failoverState.Status.Conditions, "FailoverError")
	}

	failoverComplete := (pendingUpdates == 0)
	failoverState.Status.PendingVolumeReplicationUpdates = pendingUpdates

	if failoverComplete {
		failoverState.Status.AppliedState = failoverState.Spec.FailoverState
		meta.SetStatusCondition(&failoverState.Status.Conditions, metav1.Condition{
			Type:    "FailoverComplete",
			Status:  metav1.ConditionTrue,
			Reason:  "AllReplicationsSynced",
			Message: "All VolumeReplication objects have been updated successfully.",
		})
	} else {
		meta.SetStatusCondition(&failoverState.Status.Conditions, metav1.Condition{
			Type:    "FailoverInProgress",
			Status:  metav1.ConditionTrue,
			Reason:  "SyncingReplications",
			Message: fmt.Sprintf("%d VolumeReplication objects are still syncing.", pendingUpdates),
		})
	}

	if err := r.Status().Update(ctx, failoverState); err != nil {
		log.Error(err, "Failed to update FailoverState status")
		return err
	}

	log.Info("Updated FailoverState status", "FailoverState", failoverState.Name)
	return nil
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
