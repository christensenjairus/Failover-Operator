package jobs

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// Manager handles operations related to Job resources
type Manager struct {
	client client.Client
}

// NewManager creates a new Job manager
func NewManager(client client.Client) *Manager {
	return &Manager{
		client: client,
	}
}

// IsCompleted checks if a job has completed successfully
func (m *Manager) IsCompleted(ctx context.Context, name, namespace string) (bool, error) {
	logger := log.FromContext(ctx).WithValues("job", name, "namespace", namespace)
	logger.V(1).Info("Checking if job is completed")

	// TODO: Implement completion check for jobs
	// 1. Get the job
	// 2. Check for successful completion status

	return true, nil
}

// IsFailed checks if a job has failed
func (m *Manager) IsFailed(ctx context.Context, name, namespace string) (bool, error) {
	logger := log.FromContext(ctx).WithValues("job", name, "namespace", namespace)
	logger.V(1).Info("Checking if job has failed")

	// TODO: Implement failure check for jobs
	// 1. Get the job
	// 2. Check for failure status

	return false, nil
}

// Delete deletes a job
func (m *Manager) Delete(ctx context.Context, name, namespace string) error {
	logger := log.FromContext(ctx).WithValues("job", name, "namespace", namespace)
	logger.Info("Deleting job")

	// TODO: Implement job deletion
	// 1. Get the job
	// 2. Delete the job

	return nil
}

// AddFluxAnnotation adds the flux reconcile annotation to disable automatic reconciliation
func (m *Manager) AddFluxAnnotation(ctx context.Context, name, namespace string) error {
	logger := log.FromContext(ctx).WithValues("job", name, "namespace", namespace)
	logger.Info("Adding Flux annotation to job")

	// TODO: Implement adding flux annotation to jobs
	// 1. Get the job
	// 2. Add the annotation
	// 3. Update the job

	return nil
}

// RemoveFluxAnnotation removes the flux reconcile annotation to enable automatic reconciliation
func (m *Manager) RemoveFluxAnnotation(ctx context.Context, name, namespace string) error {
	logger := log.FromContext(ctx).WithValues("job", name, "namespace", namespace)
	logger.Info("Removing Flux annotation from job")

	// TODO: Implement removing flux annotation from jobs
	// 1. Get the job
	// 2. Remove the annotation
	// 3. Update the job

	return nil
}
