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

	// Check all VolumeReplication objects referenced by this FailoverState
	for _, vrName := range failoverState.Spec.VolumeReplications {
		log.Info("Checking VolumeReplication", "VolumeReplication", vrName)
	}

	desiredState := failoverState.Spec.FailoverState // "primary" or "secondary"

	// Track how many VolumeReplication objects need updates
	var pendingVolumeReplicationUpdates int

	// Update VolumeReplication objects
	for _, vrName := range failoverState.Spec.VolumeReplications {
		pending, err := r.updateVolumeReplication(ctx, vrName, failoverState.Namespace, desiredState)
		if err != nil {
			log.Error(err, "Failed to update VolumeReplication", "VolumeReplication", vrName)
			return ctrl.Result{}, err
		}
		if pending {
			pendingVolumeReplicationUpdates++
		}
	}

	// Update VirtualService objects
	for _, vsName := range failoverState.Spec.VirtualServices {
		updated, err := r.updateVirtualService(ctx, vsName, failoverState.Namespace, desiredState)
		if err != nil {
			log.Error(err, "Failed to update VirtualService", "VirtualService", vsName)
			return ctrl.Result{}, err
		}
		if updated {
			log.Info("Updated VirtualService", "VirtualService", vsName)
		}
	}

	// Determine if failover process is complete
	failoverComplete := (pendingVolumeReplicationUpdates == 0)

	// Update status fields
	failoverState.Status.PendingVolumeReplicationUpdates = pendingVolumeReplicationUpdates

	if failoverComplete {
		// ✅ Set "FailoverComplete" only when all VolumeReplications are synced
		failoverState.Status.AppliedState = desiredState
		meta.SetStatusCondition(&failoverState.Status.Conditions, metav1.Condition{
			Type:               "FailoverComplete",
			Status:             metav1.ConditionTrue,
			Reason:             "AllReplicationsSynced",
			Message:            "All VolumeReplication objects have been updated successfully.",
			LastTransitionTime: metav1.Now(),
		})
	} else {
		// ✅ Set "FailoverInProgress" when failover is still ongoing
		meta.SetStatusCondition(&failoverState.Status.Conditions, metav1.Condition{
			Type:               "FailoverInProgress",
			Status:             metav1.ConditionTrue,
			Reason:             "SyncingReplications",
			Message:            fmt.Sprintf("%d VolumeReplication objects are still syncing.", pendingVolumeReplicationUpdates),
			LastTransitionTime: metav1.Now(),
		})
	}

	// ✅ Remove "FailoverComplete" if failover is still in progress
	if !failoverComplete {
		meta.RemoveStatusCondition(&failoverState.Status.Conditions, "FailoverComplete")
	}

	// Update the status in Kubernetes
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

	// Determine desired state
	desiredReplicationState := "secondary"
	if state == "primary" {
		desiredReplicationState = "primary"
	}

	// Extract `status.state`
	statusReplicationState := strings.ToLower(string(volumeReplication.Status.State))

	log.Info("VolumeReplication current state", "name", name, "spec.replicationState", specReplicationState, "status.state", statusReplicationState, "desiredReplicationState", desiredReplicationState)

	// If status doesn't match desired state, return as pending
	pendingStatusUpdate := statusReplicationState != desiredReplicationState

	// If spec is already correct, return without updating
	if specReplicationState == desiredReplicationState {
		log.Info("VolumeReplication spec already matches desired state, no update needed", "name", name)
		return pendingStatusUpdate, nil
	}

	// ✅ Log before updating
	log.Info("Updating VolumeReplication spec", "name", name, "newState", desiredReplicationState)

	// Update the replication state
	volumeReplication.Spec.ReplicationState = replicationv1alpha1.ReplicationState(desiredReplicationState)

	// ✅ Attempt update
	if err := r.Update(ctx, volumeReplication); err != nil {
		log.Error(err, "Failed to update VolumeReplication", "VolumeReplication", name)
		return pendingStatusUpdate, err
	}

	log.Info("Successfully updated VolumeReplication", "name", name, "newState", desiredReplicationState)

	return pendingStatusUpdate, nil
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
