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
	// FluxReconcileAnnotation is the annotation used to disable Flux reconciliation
	FluxReconcileAnnotation = "reconcile.fluxcd.io/requestedAt"

	// DisabledValue is used to disable Flux reconciliation
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

// ScaleCronJob sets the suspended state of a CronJob
// If suspend is true, the CronJob will be suspended and won't create new jobs
// If suspend is false, the CronJob will be resumed and will create jobs according to its schedule
func (m *Manager) ScaleCronJob(ctx context.Context, name, namespace string, suspend bool) error {
	logger := log.FromContext(ctx).WithValues("cronjob", name, "namespace", namespace)
	logger.Info("Scaling CronJob", "suspend", suspend)

	// Get the CronJob
	cronjob := &batchv1.CronJob{}
	err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, cronjob)
	if err != nil {
		logger.Error(err, "Failed to get CronJob")
		return err
	}

	// Update the suspend field
	cronjob.Spec.Suspend = &suspend
	err = m.client.Update(ctx, cronjob)
	if err != nil {
		logger.Error(err, "Failed to update CronJob")
		return err
	}

	logger.Info("Successfully scaled CronJob", "suspend", suspend)
	return nil
}

// Suspend suspends a CronJob so it doesn't create new jobs
// This is typically used during failover to pause scheduled jobs in the secondary cluster
func (m *Manager) Suspend(ctx context.Context, name, namespace string) error {
	logger := log.FromContext(ctx).WithValues("cronjob", name, "namespace", namespace)
	logger.Info("Suspending CronJob")
	return m.ScaleCronJob(ctx, name, namespace, true)
}

// Resume resumes a previously suspended CronJob, allowing it to create new jobs
// This is typically used during failover to activate scheduled jobs in the primary cluster
func (m *Manager) Resume(ctx context.Context, name, namespace string) error {
	logger := log.FromContext(ctx).WithValues("cronjob", name, "namespace", namespace)
	logger.Info("Resuming CronJob")
	return m.ScaleCronJob(ctx, name, namespace, false)
}

// IsReady checks if a CronJob is not suspended
// Returns true if the CronJob is not suspended (ready to schedule jobs)
func (m *Manager) IsReady(ctx context.Context, name, namespace string) (bool, error) {
	suspended, err := m.IsSuspended(ctx, name, namespace)
	if err != nil {
		return false, err
	}
	return !suspended, nil
}

// IsSuspended checks if a CronJob is suspended
// Returns true if the CronJob is suspended (not scheduling jobs)
func (m *Manager) IsSuspended(ctx context.Context, name, namespace string) (bool, error) {
	logger := log.FromContext(ctx).WithValues("cronjob", name, "namespace", namespace)
	logger.V(1).Info("Checking if CronJob is suspended")

	// Get the CronJob
	cronjob := &batchv1.CronJob{}
	err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, cronjob)
	if err != nil {
		logger.Error(err, "Failed to get CronJob")
		return false, err
	}

	// Check if suspended
	if cronjob.Spec.Suspend != nil && *cronjob.Spec.Suspend {
		return true, nil
	}
	return false, nil
}

// WaitForSuspended waits until a CronJob is suspended or timeout is reached
// Returns error if the CronJob does not become suspended within the timeout period
func (m *Manager) WaitForSuspended(ctx context.Context, name, namespace string, timeoutSeconds int) error {
	logger := log.FromContext(ctx).WithValues("cronjob", name, "namespace", namespace)
	logger.Info("Waiting for CronJob to be suspended", "timeout", timeoutSeconds)

	return wait.PollImmediate(time.Second, time.Duration(timeoutSeconds)*time.Second, func() (bool, error) {
		suspended, err := m.IsSuspended(ctx, name, namespace)
		if err != nil {
			return false, err
		}
		return suspended, nil
	})
}

// WaitForResumed waits until a CronJob is resumed or timeout is reached
// Returns error if the CronJob does not become resumed within the timeout period
func (m *Manager) WaitForResumed(ctx context.Context, name, namespace string, timeoutSeconds int) error {
	logger := log.FromContext(ctx).WithValues("cronjob", name, "namespace", namespace)
	logger.Info("Waiting for CronJob to be resumed", "timeout", timeoutSeconds)

	return wait.PollImmediate(time.Second, time.Duration(timeoutSeconds)*time.Second, func() (bool, error) {
		ready, err := m.IsReady(ctx, name, namespace)
		if err != nil {
			return false, err
		}
		return ready, nil
	})
}

// AddAnnotation adds an annotation to a CronJob
// Useful for adding metadata or configuration to the CronJob
func (m *Manager) AddAnnotation(ctx context.Context, name, namespace, key, value string) error {
	logger := log.FromContext(ctx).WithValues("cronjob", name, "namespace", namespace)
	logger.Info("Adding annotation", "key", key, "value", value)

	// Get the CronJob
	cronjob := &batchv1.CronJob{}
	err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, cronjob)
	if err != nil {
		logger.Error(err, "Failed to get CronJob")
		return err
	}

	// Add the annotation
	if cronjob.Annotations == nil {
		cronjob.Annotations = make(map[string]string)
	}
	cronjob.Annotations[key] = value

	// Update the CronJob
	err = m.client.Update(ctx, cronjob)
	if err != nil {
		logger.Error(err, "Failed to update CronJob with annotation")
		return err
	}

	return nil
}

// RemoveAnnotation removes an annotation from a CronJob
// Useful for cleaning up or changing the behavior of the CronJob
func (m *Manager) RemoveAnnotation(ctx context.Context, name, namespace, key string) error {
	logger := log.FromContext(ctx).WithValues("cronjob", name, "namespace", namespace)
	logger.Info("Removing annotation", "key", key)

	// Get the CronJob
	cronjob := &batchv1.CronJob{}
	err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, cronjob)
	if err != nil {
		logger.Error(err, "Failed to get CronJob")
		return err
	}

	// Remove the annotation if it exists
	if cronjob.Annotations != nil {
		if _, exists := cronjob.Annotations[key]; exists {
			delete(cronjob.Annotations, key)

			// Update the CronJob
			err = m.client.Update(ctx, cronjob)
			if err != nil {
				logger.Error(err, "Failed to update CronJob after removing annotation")
				return err
			}
		}
	}

	return nil
}

// GetAnnotation gets the value of an annotation from a CronJob
// Returns the value and a boolean indicating if the annotation exists
func (m *Manager) GetAnnotation(ctx context.Context, name, namespace, key string) (string, bool, error) {
	logger := log.FromContext(ctx).WithValues("cronjob", name, "namespace", namespace)
	logger.V(1).Info("Getting annotation", "key", key)

	// Get the CronJob
	cronjob := &batchv1.CronJob{}
	err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, cronjob)
	if err != nil {
		logger.Error(err, "Failed to get CronJob")
		return "", false, err
	}

	// Get the annotation if it exists
	if cronjob.Annotations != nil {
		if value, exists := cronjob.Annotations[key]; exists {
			return value, true, nil
		}
	}

	return "", false, nil
}

// AddFluxAnnotation adds the Flux reconcile annotation with a disabled value
// This prevents Flux from reconciling the resource during failover operations
func (m *Manager) AddFluxAnnotation(ctx context.Context, name, namespace string) error {
	logger := log.FromContext(ctx).WithValues("cronjob", name, "namespace", namespace)
	logger.Info("Adding Flux disable annotation")
	return m.AddAnnotation(ctx, name, namespace, FluxReconcileAnnotation, DisabledValue)
}

// RemoveFluxAnnotation removes the Flux reconcile annotation
// This allows Flux to resume reconciling the resource
func (m *Manager) RemoveFluxAnnotation(ctx context.Context, name, namespace string) error {
	logger := log.FromContext(ctx).WithValues("cronjob", name, "namespace", namespace)
	logger.Info("Removing Flux disable annotation")
	return m.RemoveAnnotation(ctx, name, namespace, FluxReconcileAnnotation)
}

// ProcessCronJobs processes a list of CronJobs, either suspending or resuming them
// If active is true, the CronJobs will be resumed
// If active is false, the CronJobs will be suspended
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
// If suspended is true, waits for all to be suspended
// If suspended is false, waits for all to be resumed
func (m *Manager) WaitForAllCronJobsState(ctx context.Context, namespace string, names []string, suspended bool, timeoutSeconds int) error {
	logger := log.FromContext(ctx).WithValues("namespace", namespace)
	stateStr := "resumed"
	if suspended {
		stateStr = "suspended"
	}
	logger.Info(fmt.Sprintf("Waiting for all CronJobs to be %s", stateStr), "count", len(names), "timeout", timeoutSeconds)

	for _, name := range names {
		var err error
		if suspended {
			err = m.WaitForSuspended(ctx, name, namespace, timeoutSeconds)
		} else {
			err = m.WaitForResumed(ctx, name, namespace, timeoutSeconds)
		}
		if err != nil {
			logger.Error(err, fmt.Sprintf("Failed waiting for CronJob to be %s", stateStr), "cronjob", name)
			return err
		}
	}

	return nil
}
