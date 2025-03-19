package flux

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	crdv1alpha1 "github.com/christensenjairus/Failover-Operator/api/v1alpha1"
)

// Manager handles operations related to Flux resources
type Manager struct {
	client client.Client
}

// NewManager creates a new Flux manager
func NewManager(client client.Client) *Manager {
	return &Manager{
		client: client,
	}
}

// HelmReleaseGVK provides the GroupVersionKind for HelmRelease
var HelmReleaseGVK = schema.GroupVersionKind{
	Group:   "helm.toolkit.fluxcd.io",
	Version: "v2beta1",
	Kind:    "HelmRelease",
}

// KustomizationGVK provides the GroupVersionKind for Kustomization
var KustomizationGVK = schema.GroupVersionKind{
	Group:   "kustomize.toolkit.fluxcd.io",
	Version: "v1",
	Kind:    "Kustomization",
}

// ProcessFluxResources handles the processing of HelmReleases and Kustomizations
func (m *Manager) ProcessFluxResources(ctx context.Context,
	helmReleases []crdv1alpha1.ResourceReference,
	kustomizations []crdv1alpha1.ResourceReference,
	policyNamespace, desiredState string) error {

	log := log.FromContext(ctx)

	// In active mode, resume Flux resources and enabling reconciliation
	if desiredState == "active" {
		log.Info("Active mode: Resuming Flux resources and enabling reconciliation")

		// Process HelmReleases
		for _, hr := range helmReleases {
			// Use namespace from resource reference or fall back to policy namespace
			ns := hr.Namespace
			if ns == "" {
				ns = policyNamespace
			}

			if err := m.updateHelmReleaseReconciliation(ctx, hr.Name, ns, true); err != nil {
				log.Error(err, "Failed to update HelmRelease reconciliation", "HelmRelease", hr.Name, "Namespace", ns)
				return err
			}
		}

		// Process Kustomizations
		for _, k := range kustomizations {
			// Use namespace from resource reference or fall back to policy namespace
			ns := k.Namespace
			if ns == "" {
				ns = policyNamespace
			}

			if err := m.updateKustomizationReconciliation(ctx, k.Name, ns, true); err != nil {
				log.Error(err, "Failed to update Kustomization reconciliation", "Kustomization", k.Name, "Namespace", ns)
				return err
			}
		}
	} else {
		// In passive mode, suspend Flux resources and add reconcile disabled annotation
		log.Info("Passive mode: Suspending Flux resources and disabling reconciliation")

		// Process HelmReleases
		for _, hr := range helmReleases {
			// Use namespace from resource reference or fall back to policy namespace
			ns := hr.Namespace
			if ns == "" {
				ns = policyNamespace
			}

			if err := m.updateHelmReleaseReconciliation(ctx, hr.Name, ns, false); err != nil {
				log.Error(err, "Failed to update HelmRelease reconciliation", "HelmRelease", hr.Name, "Namespace", ns)
				return err
			}
		}

		// Process Kustomizations
		for _, k := range kustomizations {
			// Use namespace from resource reference or fall back to policy namespace
			ns := k.Namespace
			if ns == "" {
				ns = policyNamespace
			}

			if err := m.updateKustomizationReconciliation(ctx, k.Name, ns, false); err != nil {
				log.Error(err, "Failed to update Kustomization reconciliation", "Kustomization", k.Name, "Namespace", ns)
				return err
			}
		}
	}

	return nil
}

// processHelmRelease suspends or resumes a HelmRelease using unstructured approach
func (m *Manager) processHelmRelease(ctx context.Context, name, namespace string, suspend bool) error {
	log := log.FromContext(ctx)

	// Create an unstructured object for the HelmRelease
	helmRelease := &unstructured.Unstructured{}
	helmRelease.SetGroupVersionKind(HelmReleaseGVK)

	// Get the HelmRelease
	err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, helmRelease)
	if err != nil {
		return err
	}

	// Check if already in desired state
	currentSuspend, found, err := unstructured.NestedBool(helmRelease.Object, "spec", "suspend")
	if err != nil {
		return fmt.Errorf("error reading suspend field: %w", err)
	}

	if found && currentSuspend == suspend {
		status := "suspended"
		if !suspend {
			status = "active"
		}
		log.Info(fmt.Sprintf("HelmRelease already %s", status), "HelmRelease", name, "Namespace", namespace)
		return nil
	}

	// Update the suspend field
	action := "Resuming"
	if suspend {
		action = "Suspending"
	}
	log.Info(fmt.Sprintf("%s HelmRelease", action), "HelmRelease", name, "Namespace", namespace)

	if err := unstructured.SetNestedField(helmRelease.Object, suspend, "spec", "suspend"); err != nil {
		return fmt.Errorf("error setting suspend field: %w", err)
	}

	// If resuming, force a reconciliation by adding an annotation
	if !suspend {
		annotations := helmRelease.GetAnnotations()
		if annotations == nil {
			annotations = make(map[string]string)
		}
		annotations["failover-operator.hahomelabs.com/reconcile"] = time.Now().Format(time.RFC3339)
		helmRelease.SetAnnotations(annotations)
	}

	// Update the HelmRelease
	return m.client.Update(ctx, helmRelease)
}

// processKustomization suspends or resumes a Kustomization using unstructured approach
func (m *Manager) processKustomization(ctx context.Context, name, namespace string, suspend bool) error {
	log := log.FromContext(ctx)

	// Create an unstructured object for the Kustomization
	kustomization := &unstructured.Unstructured{}
	kustomization.SetGroupVersionKind(KustomizationGVK)

	// Get the Kustomization
	err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, kustomization)
	if err != nil {
		return err
	}

	// Check if already in desired state
	currentSuspend, found, err := unstructured.NestedBool(kustomization.Object, "spec", "suspend")
	if err != nil {
		return fmt.Errorf("error reading suspend field: %w", err)
	}

	if found && currentSuspend == suspend {
		status := "suspended"
		if !suspend {
			status = "active"
		}
		log.Info(fmt.Sprintf("Kustomization already %s", status), "Kustomization", name, "Namespace", namespace)
		return nil
	}

	// Update the suspend field
	action := "Resuming"
	if suspend {
		action = "Suspending"
	}
	log.Info(fmt.Sprintf("%s Kustomization", action), "Kustomization", name, "Namespace", namespace)

	if err := unstructured.SetNestedField(kustomization.Object, suspend, "spec", "suspend"); err != nil {
		return fmt.Errorf("error setting suspend field: %w", err)
	}

	// If resuming, force a reconciliation by adding an annotation
	if !suspend {
		annotations := kustomization.GetAnnotations()
		if annotations == nil {
			annotations = make(map[string]string)
		}
		annotations["failover-operator.hahomelabs.com/reconcile"] = time.Now().Format(time.RFC3339)
		kustomization.SetAnnotations(annotations)
	}

	// Update the Kustomization
	return m.client.Update(ctx, kustomization)
}

// GetFluxStatuses gets the status of all Flux resources
func (m *Manager) GetFluxStatuses(ctx context.Context,
	helmReleases []crdv1alpha1.ResourceReference,
	kustomizations []crdv1alpha1.ResourceReference,
	policyNamespace string) []crdv1alpha1.WorkloadStatus {

	var statuses []crdv1alpha1.WorkloadStatus
	timestamp := time.Now().Format(time.RFC3339)

	// Get HelmRelease statuses
	for _, hr := range helmReleases {
		namespace := hr.Namespace
		if namespace == "" {
			namespace = policyNamespace
		}
		status := m.getHelmReleaseStatus(ctx, hr.Name, namespace)
		status.LastUpdateTime = timestamp
		statuses = append(statuses, status)
	}

	// Get Kustomization statuses
	for _, k := range kustomizations {
		namespace := k.Namespace
		if namespace == "" {
			namespace = policyNamespace
		}
		status := m.getKustomizationStatus(ctx, k.Name, namespace)
		status.LastUpdateTime = timestamp
		statuses = append(statuses, status)
	}

	return statuses
}

// getHelmReleaseStatus gets the current status of a HelmRelease
func (m *Manager) getHelmReleaseStatus(ctx context.Context, name, namespace string) crdv1alpha1.WorkloadStatus {
	log := log.FromContext(ctx)
	status := crdv1alpha1.WorkloadStatus{
		Name: name,
		Kind: "HelmRelease",
	}

	// Create an unstructured object for the HelmRelease
	helmRelease := &unstructured.Unstructured{}
	helmRelease.SetGroupVersionKind(HelmReleaseGVK)

	// Try to get the HelmRelease
	err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, helmRelease)
	if err != nil {
		if !errors.IsNotFound(err) {
			log.Error(err, "Failed to get HelmRelease status", "HelmRelease", name, "Namespace", namespace)
		}
		status.Error = fmt.Sprintf("Failed to get status: %v", err)
		status.State = "Unknown"
		return status
	}

	// Check if suspended
	suspended, found, err := unstructured.NestedBool(helmRelease.Object, "spec", "suspend")
	if err != nil || !found {
		status.Error = "Could not determine suspend state"
		status.State = "Unknown"
		return status
	}

	if suspended {
		status.State = "Suspended"
	} else {
		status.State = "Active"
	}

	return status
}

// getKustomizationStatus gets the current status of a Kustomization
func (m *Manager) getKustomizationStatus(ctx context.Context, name, namespace string) crdv1alpha1.WorkloadStatus {
	log := log.FromContext(ctx)
	status := crdv1alpha1.WorkloadStatus{
		Name: name,
		Kind: "Kustomization",
	}

	// Create an unstructured object for the Kustomization
	kustomization := &unstructured.Unstructured{}
	kustomization.SetGroupVersionKind(KustomizationGVK)

	// Try to get the Kustomization
	err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, kustomization)
	if err != nil {
		if !errors.IsNotFound(err) {
			log.Error(err, "Failed to get Kustomization status", "Kustomization", name, "Namespace", namespace)
		}
		status.Error = fmt.Sprintf("Failed to get status: %v", err)
		status.State = "Unknown"
		return status
	}

	// Check if suspended
	suspended, found, err := unstructured.NestedBool(kustomization.Object, "spec", "suspend")
	if err != nil || !found {
		status.Error = "Could not determine suspend state"
		status.State = "Unknown"
		return status
	}

	if suspended {
		status.State = "Suspended"
	} else {
		status.State = "Active"
	}

	return status
}

// suspendHelmRelease suspends a HelmRelease
func (m *Manager) suspendHelmRelease(ctx context.Context, name, namespace string) error {
	return m.processHelmRelease(ctx, name, namespace, true)
}

// resumeHelmRelease resumes a HelmRelease
func (m *Manager) resumeHelmRelease(ctx context.Context, name, namespace string) error {
	return m.processHelmRelease(ctx, name, namespace, false)
}

// suspendKustomization suspends a Kustomization
func (m *Manager) suspendKustomization(ctx context.Context, name, namespace string) error {
	return m.processKustomization(ctx, name, namespace, true)
}

// resumeKustomization resumes a Kustomization
func (m *Manager) resumeKustomization(ctx context.Context, name, namespace string) error {
	return m.processKustomization(ctx, name, namespace, false)
}

// updateHelmReleaseReconciliation updates the suspension and reconciliation status of a HelmRelease
func (m *Manager) updateHelmReleaseReconciliation(ctx context.Context, name, namespace string, enableReconciliation bool) error {
	log := log.FromContext(ctx)

	// Get the HelmRelease
	hr := &unstructured.Unstructured{}
	hr.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "helm.toolkit.fluxcd.io",
		Version: "v2beta1",
		Kind:    "HelmRelease",
	})

	err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, hr)
	if err != nil {
		return client.IgnoreNotFound(err)
	}

	// Add or remove the reconcile annotation
	annotations := hr.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}

	modified := false

	if enableReconciliation {
		// If we're enabling reconciliation, remove the reconcile: disabled annotation
		if _, exists := annotations["kustomize.toolkit.fluxcd.io/reconcile"]; exists {
			delete(annotations, "kustomize.toolkit.fluxcd.io/reconcile")
			modified = true
			log.Info("Removed reconcile annotation from HelmRelease", "name", name, "namespace", namespace)
		}

		// Also unsuspend the resource
		suspend, found, err := unstructured.NestedBool(hr.Object, "spec", "suspend")
		if err != nil {
			return err
		}

		if found && suspend {
			err = unstructured.SetNestedField(hr.Object, false, "spec", "suspend")
			if err != nil {
				return err
			}
			modified = true
			log.Info("Resumed HelmRelease", "name", name, "namespace", namespace)
		}
	} else {
		// If we're disabling reconciliation, add the annotation
		if annotations["kustomize.toolkit.fluxcd.io/reconcile"] != "disabled" {
			annotations["kustomize.toolkit.fluxcd.io/reconcile"] = "disabled"
			modified = true
			log.Info("Added reconcile: disabled annotation to HelmRelease", "name", name, "namespace", namespace)
		}

		// Also suspend the resource
		suspend, found, err := unstructured.NestedBool(hr.Object, "spec", "suspend")
		if err != nil {
			return err
		}

		if !found || !suspend {
			err = unstructured.SetNestedField(hr.Object, true, "spec", "suspend")
			if err != nil {
				return err
			}
			modified = true
			log.Info("Suspended HelmRelease", "name", name, "namespace", namespace)
		}
	}

	if modified {
		hr.SetAnnotations(annotations)
		if err := m.client.Update(ctx, hr); err != nil {
			return err
		}
	}

	return nil
}

// updateKustomizationReconciliation updates the suspension and reconciliation status of a Kustomization
func (m *Manager) updateKustomizationReconciliation(ctx context.Context, name, namespace string, enableReconciliation bool) error {
	log := log.FromContext(ctx)

	// Get the Kustomization
	k := &unstructured.Unstructured{}
	k.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "kustomize.toolkit.fluxcd.io",
		Version: "v1beta2",
		Kind:    "Kustomization",
	})

	err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, k)
	if err != nil {
		return client.IgnoreNotFound(err)
	}

	// Add or remove the reconcile annotation
	annotations := k.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}

	modified := false

	if enableReconciliation {
		// If we're enabling reconciliation, remove the reconcile: disabled annotation
		if _, exists := annotations["kustomize.toolkit.fluxcd.io/reconcile"]; exists {
			delete(annotations, "kustomize.toolkit.fluxcd.io/reconcile")
			modified = true
			log.Info("Removed reconcile annotation from Kustomization", "name", name, "namespace", namespace)
		}

		// Also unsuspend the resource
		suspend, found, err := unstructured.NestedBool(k.Object, "spec", "suspend")
		if err != nil {
			return err
		}

		if found && suspend {
			err = unstructured.SetNestedField(k.Object, false, "spec", "suspend")
			if err != nil {
				return err
			}
			modified = true
			log.Info("Resumed Kustomization", "name", name, "namespace", namespace)
		}
	} else {
		// If we're disabling reconciliation, add the annotation
		if annotations["kustomize.toolkit.fluxcd.io/reconcile"] != "disabled" {
			annotations["kustomize.toolkit.fluxcd.io/reconcile"] = "disabled"
			modified = true
			log.Info("Added reconcile: disabled annotation to Kustomization", "name", name, "namespace", namespace)
		}

		// Also suspend the resource
		suspend, found, err := unstructured.NestedBool(k.Object, "spec", "suspend")
		if err != nil {
			return err
		}

		if !found || !suspend {
			err = unstructured.SetNestedField(k.Object, true, "spec", "suspend")
			if err != nil {
				return err
			}
			modified = true
			log.Info("Suspended Kustomization", "name", name, "namespace", namespace)
		}
	}

	if modified {
		k.SetAnnotations(annotations)
		if err := m.client.Update(ctx, k); err != nil {
			return err
		}
	}

	return nil
}
