package cronjobs

import (
	"context"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// FluxReconcileAnnotation is the annotation used by Flux to control reconciliation
const FluxReconcileAnnotation = "kustomize.toolkit.fluxcd.io/reconcile"

// DisabledValue is the value for the Flux reconcile annotation to disable reconciliation
const DisabledValue = "disabled"

// Manager handles operations related to CronJob resources
// This manager provides methods to control CronJob suspension states during failover operations
type Manager struct {
	client client.Client
}

// NewManager creates a new CronJob manager
// The client is used to interact with the Kubernetes API
func NewManager(client client.Client) *Manager {
	return &Manager{
		client: client,
	}
}

// ScaleCronJob suspends or unsuspends a cronjob
// This is the core scaling method for CronJobs - unlike Deployments/StatefulSets,
// CronJobs are "scaled" by changing their suspended flag rather than a replica count
func (m *Manager) ScaleCronJob(ctx context.Context, name, namespace string, suspended bool) error {
	logger := log.FromContext(ctx).WithValues("cronjob", name, "namespace", namespace)
	logger.Info("Scaling cronjob", "suspended", suspended)

	// Get the cronjob
	cronjob := &batchv1.CronJob{}
	if err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, cronjob); err != nil {
		logger.Error(err, "Failed to get cronjob")
		return err
	}

	// Check if already in desired state
	if cronjob.Spec.Suspend != nil && *cronjob.Spec.Suspend == suspended {
		logger.Info("CronJob already in desired state", "suspended", suspended)
		return nil
	}

	// Update the suspended flag
	cronjob.Spec.Suspend = &suspended

	// Update the cronjob
	if err := m.client.Update(ctx, cronjob); err != nil {
		logger.Error(err, "Failed to update cronjob")
		return err
	}

	logger.Info("Successfully scaled cronjob", "suspended", suspended)
	return nil
}

// Suspend suspends a cronjob
// Used during failover to STANDBY state to prevent jobs from running
func (m *Manager) Suspend(ctx context.Context, name, namespace string) error {
	return m.ScaleCronJob(ctx, name, namespace, true)
}

// Resume resumes a suspended cronjob
// Used during failover to PRIMARY state to allow jobs to run
func (m *Manager) Resume(ctx context.Context, name, namespace string) error {
	return m.ScaleCronJob(ctx, name, namespace, false)
}

// WaitForSuspended waits until a cronjob is suspended
// This is useful during failover to ensure the cronjob is fully suspended before proceeding
func (m *Manager) WaitForSuspended(ctx context.Context, name, namespace string, timeout int) error {
	logger := log.FromContext(ctx).WithValues("cronjob", name, "namespace", namespace)
	logger.Info("Waiting for cronjob to be suspended", "timeout", timeout)

	return wait.PollImmediate(time.Second, time.Duration(timeout)*time.Second, func() (bool, error) {
		suspended, err := m.IsSuspended(ctx, name, namespace)
		if err != nil {
			return false, err
		}
		return suspended, nil
	})
}

// WaitForResumed waits until a cronjob is resumed
// This is useful during failover to ensure the cronjob is fully resumed before proceeding
func (m *Manager) WaitForResumed(ctx context.Context, name, namespace string, timeout int) error {
	logger := log.FromContext(ctx).WithValues("cronjob", name, "namespace", namespace)
	logger.Info("Waiting for cronjob to be resumed", "timeout", timeout)

	return wait.PollImmediate(time.Second, time.Duration(timeout)*time.Second, func() (bool, error) {
		ready, err := m.IsReady(ctx, name, namespace)
		if err != nil {
			return false, err
		}
		return ready, nil
	})
}

// IsReady checks if a cronjob is ready (not suspended)
// This is used to determine if a cronjob is in the appropriate state for PRIMARY role
func (m *Manager) IsReady(ctx context.Context, name, namespace string) (bool, error) {
	logger := log.FromContext(ctx).WithValues("cronjob", name, "namespace", namespace)
	logger.V(1).Info("Checking if cronjob is ready")

	cronjob := &batchv1.CronJob{}
	if err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, cronjob); err != nil {
		logger.Error(err, "Failed to get cronjob")
		return false, err
	}

	// A cronjob is ready when it is not suspended
	return cronjob.Spec.Suspend == nil || !*cronjob.Spec.Suspend, nil
}

// IsSuspended checks if a cronjob is suspended
// This is used to determine if a cronjob is in the appropriate state for STANDBY role
func (m *Manager) IsSuspended(ctx context.Context, name, namespace string) (bool, error) {
	logger := log.FromContext(ctx).WithValues("cronjob", name, "namespace", namespace)
	logger.V(1).Info("Checking if cronjob is suspended")

	cronjob := &batchv1.CronJob{}
	if err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, cronjob); err != nil {
		logger.Error(err, "Failed to get cronjob")
		return false, err
	}

	// A cronjob is suspended when the suspend flag is true
	return cronjob.Spec.Suspend != nil && *cronjob.Spec.Suspend, nil
}

// AddFluxAnnotation adds the flux reconcile annotation to disable automatic reconciliation
// This is used to prevent Flux from overriding our manual scaling during failover
func (m *Manager) AddFluxAnnotation(ctx context.Context, name, namespace string) error {
	logger := log.FromContext(ctx).WithValues("cronjob", name, "namespace", namespace)
	logger.Info("Adding Flux annotation to cronjob")

	return m.AddAnnotation(ctx, name, namespace, FluxReconcileAnnotation, DisabledValue)
}

// RemoveFluxAnnotation removes the flux reconcile annotation to enable automatic reconciliation
// This is used to allow Flux to resume normal reconciliation after failover is complete
func (m *Manager) RemoveFluxAnnotation(ctx context.Context, name, namespace string) error {
	logger := log.FromContext(ctx).WithValues("cronjob", name, "namespace", namespace)
	logger.Info("Removing Flux annotation from cronjob")

	return m.RemoveAnnotation(ctx, name, namespace, FluxReconcileAnnotation)
}

// AddAnnotation adds a specific annotation to a cronjob
// This is a general-purpose method for adding any annotation to a cronjob
func (m *Manager) AddAnnotation(ctx context.Context, name, namespace, annotationKey, annotationValue string) error {
	logger := log.FromContext(ctx).WithValues("cronjob", name, "namespace", namespace)
	logger.Info("Adding annotation to cronjob", "key", annotationKey, "value", annotationValue)

	// Get the cronjob
	cronjob := &batchv1.CronJob{}
	if err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, cronjob); err != nil {
		logger.Error(err, "Failed to get cronjob")
		return err
	}

	// Initialize annotations map if it doesn't exist
	if cronjob.Annotations == nil {
		cronjob.Annotations = make(map[string]string)
	}

	// Check if the annotation already exists with the same value
	if value, exists := cronjob.Annotations[annotationKey]; exists && value == annotationValue {
		logger.Info("Annotation already exists with the same value", "key", annotationKey, "value", annotationValue)
		return nil
	}

	// Add the annotation
	cronjob.Annotations[annotationKey] = annotationValue

	// Update the cronjob
	if err := m.client.Update(ctx, cronjob); err != nil {
		logger.Error(err, "Failed to update cronjob with annotation")
		return err
	}

	logger.Info("Successfully added annotation to cronjob", "key", annotationKey, "value", annotationValue)
	return nil
}

// RemoveAnnotation removes a specific annotation from a cronjob
// This is a general-purpose method for removing any annotation from a cronjob
func (m *Manager) RemoveAnnotation(ctx context.Context, name, namespace, annotationKey string) error {
	logger := log.FromContext(ctx).WithValues("cronjob", name, "namespace", namespace)
	logger.Info("Removing annotation from cronjob", "key", annotationKey)

	// Get the cronjob
	cronjob := &batchv1.CronJob{}
	if err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, cronjob); err != nil {
		logger.Error(err, "Failed to get cronjob")
		return err
	}

	// Check if the annotation exists
	if cronjob.Annotations == nil || cronjob.Annotations[annotationKey] == "" {
		logger.Info("Annotation doesn't exist, nothing to remove", "key", annotationKey)
		return nil
	}

	// Remove the annotation
	delete(cronjob.Annotations, annotationKey)

	// Update the cronjob
	if err := m.client.Update(ctx, cronjob); err != nil {
		logger.Error(err, "Failed to update cronjob after removing annotation")
		return err
	}

	logger.Info("Successfully removed annotation from cronjob", "key", annotationKey)
	return nil
}

// GetAnnotation gets the value of a specific annotation from a cronjob
// This is a general-purpose method for retrieving any annotation from a cronjob
func (m *Manager) GetAnnotation(ctx context.Context, name, namespace, annotationKey string) (string, bool, error) {
	logger := log.FromContext(ctx).WithValues("cronjob", name, "namespace", namespace)
	logger.V(1).Info("Getting annotation from cronjob", "key", annotationKey)

	// Get the cronjob
	cronjob := &batchv1.CronJob{}
	if err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, cronjob); err != nil {
		logger.Error(err, "Failed to get cronjob")
		return "", false, err
	}

	// Check if annotations map exists
	if cronjob.Annotations == nil {
		return "", false, nil
	}

	// Get the annotation value
	value, exists := cronjob.Annotations[annotationKey]
	return value, exists, nil
}