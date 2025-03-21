package cronjobs

import (
	"context"
	"fmt"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// Constants for annotations
const (
	// FluxReconcileAnnotation is the annotation used by Flux to control reconciliation
	FluxReconcileAnnotation = "kustomize.toolkit.fluxcd.io/reconcile"

	// DisabledValue is the value for the Flux reconcile annotation to disable reconciliation
	DisabledValue = "disabled"
)

// Manager handles operations related to CronJob resources
// This manager provides methods to suspend, resume, and check the status of CronJobs
type Manager struct {
	// Kubernetes client for API interactions
	client client.Client
}

// NewManager creates a new CronJob manager
// The client is used to interact with the Kubernetes API server
func NewManager(client client.Client) *Manager {
	return &Manager{
		client: client,
	}
}

// ScaleCronJob scales a CronJob by setting the suspend field
// This is used during failover to suspend or resume CronJobs based on whether
// the cluster is PRIMARY or STANDBY
func (m *Manager) ScaleCronJob(ctx context.Context, name, namespace string, suspend bool) error {
	logger := log.FromContext(ctx).WithValues("cronjob", name, "namespace", namespace)
	action := "resuming"
	if suspend {
		action = "suspending"
	}
	logger.Info(action + " CronJob")

	// Get the CronJob
	cronjob := &batchv1.CronJob{}
	if err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, cronjob); err != nil {
		logger.Error(err, "Failed to get CronJob")
		return err
	}

	// Update the suspend field
	cronjob.Spec.Suspend = &suspend

	// Update the CronJob
	if err := m.client.Update(ctx, cronjob); err != nil {
		logger.Error(err, "Failed to update CronJob suspend status")
		return err
	}

	logger.Info("Successfully " + action + " CronJob")
	return nil
}

// Suspend suspends a CronJob to prevent it from running scheduled jobs
// This is typically used in STANDBY clusters to ensure the CronJob is not running
func (m *Manager) Suspend(ctx context.Context, name, namespace string) error {
	logger := log.FromContext(ctx).WithValues("cronjob", name, "namespace", namespace)
	logger.Info("Suspending CronJob")

	return m.ScaleCronJob(ctx, name, namespace, true)
}

// Resume resumes a CronJob to allow it to run scheduled jobs
// This is typically used in PRIMARY clusters to ensure the CronJob is running
func (m *Manager) Resume(ctx context.Context, name, namespace string) error {
	logger := log.FromContext(ctx).WithValues("cronjob", name, "namespace", namespace)
	logger.Info("Resuming CronJob")

	return m.ScaleCronJob(ctx, name, namespace, false)
}

// IsReady checks if a CronJob is in the desired state (suspended or not)
// A CronJob is considered ready when its suspend status matches the desired state
func (m *Manager) IsReady(ctx context.Context, name, namespace string, shouldBeSuspended bool) (bool, error) {
	logger := log.FromContext(ctx).WithValues("cronjob", name, "namespace", namespace)
	logger.V(1).Info("Checking if CronJob is ready", "shouldBeSuspended", shouldBeSuspended)

	// Get the CronJob
	cronjob := &batchv1.CronJob{}
	if err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, cronjob); err != nil {
		logger.Error(err, "Failed to get CronJob")
		return false, err
	}

	// Check if the suspend status matches the desired state
	isSuspended := cronjob.Spec.Suspend != nil && *cronjob.Spec.Suspend
	isReady := isSuspended == shouldBeSuspended

	logger.V(1).Info("CronJob readiness check", "ready", isReady, "isSuspended", isSuspended, "shouldBeSuspended", shouldBeSuspended)
	return isReady, nil
}

// IsSuspended checks if a CronJob is suspended
// This is used to verify that a CronJob in a STANDBY cluster is properly suspended
func (m *Manager) IsSuspended(ctx context.Context, name, namespace string) (bool, error) {
	logger := log.FromContext(ctx).WithValues("cronjob", name, "namespace", namespace)
	logger.V(1).Info("Checking if CronJob is suspended")

	// Get the CronJob
	cronjob := &batchv1.CronJob{}
	if err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, cronjob); err != nil {
		logger.Error(err, "Failed to get CronJob")
		return false, err
	}

	isSuspended := cronjob.Spec.Suspend != nil && *cronjob.Spec.Suspend
	logger.V(1).Info("CronJob suspend check", "suspended", isSuspended)
	return isSuspended, nil
}

// WaitForSuspended waits for a CronJob to be suspended
// This is useful during failover to ensure CronJobs are fully suspended before proceeding
func (m *Manager) WaitForSuspended(ctx context.Context, name, namespace string, timeout time.Duration) error {
	logger := log.FromContext(ctx).WithValues("cronjob", name, "namespace", namespace)
	logger.Info("Waiting for CronJob to be suspended", "timeout", timeout)

	return wait.PollImmediate(2*time.Second, timeout, func() (bool, error) {
		suspended, err := m.IsSuspended(ctx, name, namespace)
		if err != nil {
			logger.Error(err, "Failed to check if CronJob is suspended")
			return false, nil // Continue polling
		}

		if suspended {
			logger.Info("CronJob is suspended")
			return true, nil
		}

		logger.V(1).Info("CronJob not yet suspended")
		return false, nil
	})
}

// WaitForResumed waits for a CronJob to be resumed
// This is useful during failover to ensure CronJobs are fully resumed before proceeding
func (m *Manager) WaitForResumed(ctx context.Context, name, namespace string, timeout time.Duration) error {
	logger := log.FromContext(ctx).WithValues("cronjob", name, "namespace", namespace)
	logger.Info("Waiting for CronJob to be resumed", "timeout", timeout)

	return wait.PollImmediate(2*time.Second, timeout, func() (bool, error) {
		suspended, err := m.IsSuspended(ctx, name, namespace)
		if err != nil {
			logger.Error(err, "Failed to check if CronJob is resumed")
			return false, nil // Continue polling
		}

		if !suspended {
			logger.Info("CronJob is resumed")
			return true, nil
		}

		logger.V(1).Info("CronJob not yet resumed")
		return false, nil
	})
}

// AddAnnotation adds an annotation to a CronJob
// Used for various purposes such as disabling Flux reconciliation or providing metadata
func (m *Manager) AddAnnotation(ctx context.Context, name, namespace, key, value string) error {
	logger := log.FromContext(ctx).WithValues("cronjob", name, "namespace", namespace)
	logger.Info("Adding annotation to CronJob", "key", key, "value", value)

	// Get the CronJob
	cronjob := &batchv1.CronJob{}
	if err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, cronjob); err != nil {
		logger.Error(err, "Failed to get CronJob")
		return err
	}

	// Add the annotation
	if cronjob.Annotations == nil {
		cronjob.Annotations = make(map[string]string)
	}
	cronjob.Annotations[key] = value

	// Update the CronJob
	if err := m.client.Update(ctx, cronjob); err != nil {
		logger.Error(err, "Failed to update CronJob annotations")
		return err
	}

	logger.Info("Successfully added annotation to CronJob", "key", key)
	return nil
}

// RemoveAnnotation removes an annotation from a CronJob
// Used to clean up annotations that are no longer needed
func (m *Manager) RemoveAnnotation(ctx context.Context, name, namespace, key string) error {
	logger := log.FromContext(ctx).WithValues("cronjob", name, "namespace", namespace)
	logger.Info("Removing annotation from CronJob", "key", key)

	// Get the CronJob
	cronjob := &batchv1.CronJob{}
	if err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, cronjob); err != nil {
		logger.Error(err, "Failed to get CronJob")
		return err
	}

	// Remove the annotation if it exists
	if cronjob.Annotations != nil {
		if _, exists := cronjob.Annotations[key]; exists {
			delete(cronjob.Annotations, key)

			// Update the CronJob
			if err := m.client.Update(ctx, cronjob); err != nil {
				logger.Error(err, "Failed to update CronJob annotations")
				return err
			}

			logger.Info("Successfully removed annotation from CronJob", "key", key)
		} else {
			logger.Info("Annotation does not exist on CronJob", "key", key)
		}
	}

	return nil
}

// GetAnnotation gets an annotation from a CronJob
// Returns the value and a boolean indicating if the annotation exists
func (m *Manager) GetAnnotation(ctx context.Context, name, namespace, key string) (string, bool, error) {
	logger := log.FromContext(ctx).WithValues("cronjob", name, "namespace", namespace)
	logger.V(1).Info("Getting annotation from CronJob", "key", key)

	// Get the CronJob
	cronjob := &batchv1.CronJob{}
	if err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, cronjob); err != nil {
		logger.Error(err, "Failed to get CronJob")
		return "", false, err
	}

	// Get the annotation if it exists
	if cronjob.Annotations != nil {
		value, exists := cronjob.Annotations[key]
		logger.V(1).Info("CronJob annotation status", "key", key, "exists", exists, "value", value)
		return value, exists, nil
	}

	logger.V(1).Info("CronJob has no annotations")
	return "", false, nil
}

// AddFluxAnnotation adds the Flux reconcile annotation to a CronJob with a value of "disabled"
// This is used to prevent Flux from reconciling the CronJob during failover
func (m *Manager) AddFluxAnnotation(ctx context.Context, name, namespace string) error {
	logger := log.FromContext(ctx).WithValues("cronjob", name, "namespace", namespace)
	logger.Info("Adding Flux reconcile annotation to CronJob")

	return m.AddAnnotation(ctx, name, namespace, FluxReconcileAnnotation, DisabledValue)
}

// RemoveFluxAnnotation removes the Flux reconcile annotation from a CronJob
// This allows Flux to resume reconciling the CronJob after failover is complete
func (m *Manager) RemoveFluxAnnotation(ctx context.Context, name, namespace string) error {
	logger := log.FromContext(ctx).WithValues("cronjob", name, "namespace", namespace)
	logger.Info("Removing Flux reconcile annotation from CronJob")

	return m.RemoveAnnotation(ctx, name, namespace, FluxReconcileAnnotation)
}

// ProcessCronJobs processes a list of CronJobs
// Suspends or resumes them based on the active parameter
func (m *Manager) ProcessCronJobs(ctx context.Context, namespace string, names []string, active bool) {
	logger := log.FromContext(ctx).WithValues("namespace", namespace)
	logger.Info("Processing CronJobs", "count", len(names), "active", active)

	for _, name := range names {
		if active {
			if err := m.Resume(ctx, name, namespace); err != nil {
				logger.Error(err, "Failed to resume CronJob", "cronjob", name)
			}
		} else {
			if err := m.Suspend(ctx, name, namespace); err != nil {
				logger.Error(err, "Failed to suspend CronJob", "cronjob", name)
			}
		}
	}
}

// WaitForAllCronJobsState waits for all CronJobs to reach the desired state
// Used during failover to ensure all CronJobs are properly suspended or resumed
func (m *Manager) WaitForAllCronJobsState(ctx context.Context, namespace string, names []string, shouldBeSuspended bool, timeout time.Duration) error {
	logger := log.FromContext(ctx).WithValues("namespace", namespace)
	stateText := "resumed"
	if shouldBeSuspended {
		stateText = "suspended"
	}
	logger.Info("Waiting for all CronJobs to be "+stateText, "count", len(names), "timeout", timeout)

	for _, name := range names {
		var err error
		if shouldBeSuspended {
			err = m.WaitForSuspended(ctx, name, namespace, timeout)
		} else {
			err = m.WaitForResumed(ctx, name, namespace, timeout)
		}

		if err != nil {
			logger.Error(err, "Timeout waiting for CronJob state change", "cronjob", name, "targetState", stateText)
			return fmt.Errorf("timeout waiting for CronJob %s to be %s: %w", name, stateText, err)
		}
	}

	logger.Info("All CronJobs are now " + stateText)
	return nil
}
