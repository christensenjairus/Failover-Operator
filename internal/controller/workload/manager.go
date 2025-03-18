package workload

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	crdv1alpha1 "github.com/christensenjairus/Failover-Operator/api/v1alpha1"
)

// Manager handles operations related to workload resources
type Manager struct {
	client client.Client
}

// NewManager creates a new workload manager
func NewManager(client client.Client) *Manager {
	return &Manager{
		client: client,
	}
}

// ProcessWorkloads handles appropriate workload actions based on policy state
func (m *Manager) ProcessWorkloads(ctx context.Context,
	deployments, statefulSets, cronJobs []string,
	namespace, desiredState string) error {

	logger := log.FromContext(ctx).WithName("workload-manager")

	if desiredState == "primary" {
		// In primary mode, Flux will handle restoring workloads
		// Nothing to do here
		return nil
	}

	// In secondary mode, scale down all workloads
	logger.Info("Processing workloads in secondary mode",
		"deployments", len(deployments),
		"statefulSets", len(statefulSets),
		"cronJobs", len(cronJobs))

	// Process deployments
	for _, name := range deployments {
		if err := m.ensureDeploymentScaledDown(ctx, name, namespace); err != nil {
			logger.Error(err, "Failed to scale down deployment", "name", name, "namespace", namespace)
		}
	}

	// Process statefulsets
	for _, name := range statefulSets {
		if err := m.ensureStatefulSetScaledDown(ctx, name, namespace); err != nil {
			logger.Error(err, "Failed to scale down statefulset", "name", name, "namespace", namespace)
		}
	}

	// Process cronjobs
	for _, name := range cronJobs {
		if err := m.ensureCronJobSuspended(ctx, name, namespace); err != nil {
			logger.Error(err, "Failed to suspend cronjob", "name", name, "namespace", namespace)
		}
	}

	return nil
}

// scaleDeployment scales a Deployment to the specified number of replicas
func (m *Manager) scaleDeployment(ctx context.Context, namespace, name string, replicas int32) error {
	log := log.FromContext(ctx)
	deployment := &appsv1.Deployment{}

	err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, deployment)
	if err != nil {
		return client.IgnoreNotFound(err)
	}

	if deployment.Spec.Replicas != nil && *deployment.Spec.Replicas == replicas {
		log.Info("Deployment already at desired replicas", "Deployment", name, "Replicas", replicas)
		return nil
	}

	log.Info("Scaling Deployment", "Deployment", name, "From", deployment.Spec.Replicas, "To", replicas)
	deployment.Spec.Replicas = &replicas

	return m.client.Update(ctx, deployment)
}

// scaleStatefulSet scales a StatefulSet to the specified number of replicas
func (m *Manager) scaleStatefulSet(ctx context.Context, namespace, name string, replicas int32) error {
	log := log.FromContext(ctx)
	statefulSet := &appsv1.StatefulSet{}

	err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, statefulSet)
	if err != nil {
		return client.IgnoreNotFound(err)
	}

	if statefulSet.Spec.Replicas != nil && *statefulSet.Spec.Replicas == replicas {
		log.Info("StatefulSet already at desired replicas", "StatefulSet", name, "Replicas", replicas)
		return nil
	}

	log.Info("Scaling StatefulSet", "StatefulSet", name, "From", statefulSet.Spec.Replicas, "To", replicas)
	statefulSet.Spec.Replicas = &replicas

	return m.client.Update(ctx, statefulSet)
}

// suspendCronJob suspends or activates a CronJob
func (m *Manager) suspendCronJob(ctx context.Context, namespace, name string, suspend bool) error {
	log := log.FromContext(ctx)
	cronJob := &batchv1.CronJob{}

	err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, cronJob)
	if err != nil {
		return client.IgnoreNotFound(err)
	}

	if cronJob.Spec.Suspend != nil && *cronJob.Spec.Suspend == suspend {
		status := "suspended"
		if !suspend {
			status = "active"
		}
		log.Info(fmt.Sprintf("CronJob already %s", status), "CronJob", name)
		return nil
	}

	action := "Suspending"
	if !suspend {
		action = "Activating"
	}
	log.Info(fmt.Sprintf("%s CronJob", action), "CronJob", name)
	cronJob.Spec.Suspend = &suspend

	return m.client.Update(ctx, cronJob)
}

// ensureDeploymentScaledDown scales down a deployment to 0 replicas
func (m *Manager) ensureDeploymentScaledDown(ctx context.Context, name, namespace string) error {
	logger := log.FromContext(ctx).WithName("workload-manager")

	deployment := &appsv1.Deployment{}
	if err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, deployment); err != nil {
		if apierrors.IsNotFound(err) {
			return nil // Deployment doesn't exist, nothing to do
		}
		return fmt.Errorf("failed to get deployment %s: %w", name, err)
	}

	// Check if already scaled down
	if deployment.Spec.Replicas != nil && *deployment.Spec.Replicas == 0 {
		return nil
	}

	// Scale down the deployment
	zero := int32(0)
	deployment.Spec.Replicas = &zero
	if err := m.client.Update(ctx, deployment); err != nil {
		return fmt.Errorf("failed to scale down deployment %s: %w", name, err)
	}

	logger.Info("Scaled down deployment", "name", name, "namespace", namespace)
	return nil
}

// ensureStatefulSetScaledDown scales down a statefulset to 0 replicas
func (m *Manager) ensureStatefulSetScaledDown(ctx context.Context, name, namespace string) error {
	logger := log.FromContext(ctx).WithName("workload-manager")

	statefulset := &appsv1.StatefulSet{}
	if err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, statefulset); err != nil {
		if apierrors.IsNotFound(err) {
			return nil // StatefulSet doesn't exist, nothing to do
		}
		return fmt.Errorf("failed to get statefulset %s: %w", name, err)
	}

	// Check if already scaled down
	if statefulset.Spec.Replicas != nil && *statefulset.Spec.Replicas == 0 {
		return nil
	}

	// Scale down the statefulset
	zero := int32(0)
	statefulset.Spec.Replicas = &zero
	if err := m.client.Update(ctx, statefulset); err != nil {
		return fmt.Errorf("failed to scale down statefulset %s: %w", name, err)
	}

	logger.Info("Scaled down statefulset", "name", name, "namespace", namespace)
	return nil
}

// ensureCronJobSuspended suspends a cronjob
func (m *Manager) ensureCronJobSuspended(ctx context.Context, name, namespace string) error {
	logger := log.FromContext(ctx).WithName("workload-manager")

	cronjob := &batchv1.CronJob{}
	if err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, cronjob); err != nil {
		if apierrors.IsNotFound(err) {
			return nil // CronJob doesn't exist, nothing to do
		}
		return fmt.Errorf("failed to get cronjob %s: %w", name, err)
	}

	// Check if already suspended
	if cronjob.Spec.Suspend != nil && *cronjob.Spec.Suspend {
		return nil
	}

	// Suspend the cronjob
	suspend := true
	cronjob.Spec.Suspend = &suspend
	if err := m.client.Update(ctx, cronjob); err != nil {
		return fmt.Errorf("failed to suspend cronjob %s: %w", name, err)
	}

	logger.Info("Suspended cronjob", "name", name, "namespace", namespace)
	return nil
}

// GetWorkloadStatuses gets the status of workloads managed by this manager
func (m *Manager) GetWorkloadStatuses(ctx context.Context, namespace string,
	deployments, statefulSets, cronJobs []string) []crdv1alpha1.WorkloadStatus {

	statuses := []crdv1alpha1.WorkloadStatus{}

	// Process deployments
	for _, name := range deployments {
		status := m.getDeploymentStatus(ctx, name, namespace)
		statuses = append(statuses, status)
	}

	// Process statefulsets
	for _, name := range statefulSets {
		status := m.getStatefulSetStatus(ctx, name, namespace)
		statuses = append(statuses, status)
	}

	// Process cronjobs
	for _, name := range cronJobs {
		status := m.getCronJobStatus(ctx, name, namespace)
		statuses = append(statuses, status)
	}

	return statuses
}

// getDeploymentStatus gets the status of a deployment
func (m *Manager) getDeploymentStatus(ctx context.Context, name, namespace string) crdv1alpha1.WorkloadStatus {
	timestamp := time.Now().Format(time.RFC3339)
	status := crdv1alpha1.WorkloadStatus{
		Name:           name,
		Kind:           "Deployment",
		LastUpdateTime: timestamp,
	}

	deployment := &appsv1.Deployment{}
	if err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, deployment); err != nil {
		if apierrors.IsNotFound(err) {
			status.State = "Not Found"
			status.Error = "Deployment does not exist"
		} else {
			status.State = "Error"
			status.Error = fmt.Sprintf("Failed to get deployment: %v", err)
		}
		return status
	}

	// Check if scaled down
	if deployment.Spec.Replicas != nil && *deployment.Spec.Replicas == 0 {
		status.State = "Scaled Down"
	} else {
		status.State = "Active"
		status.Error = fmt.Sprintf("Deployment has %d replicas", *deployment.Spec.Replicas)
	}

	return status
}

// getStatefulSetStatus gets the status of a statefulset
func (m *Manager) getStatefulSetStatus(ctx context.Context, name, namespace string) crdv1alpha1.WorkloadStatus {
	timestamp := time.Now().Format(time.RFC3339)
	status := crdv1alpha1.WorkloadStatus{
		Name:           name,
		Kind:           "StatefulSet",
		LastUpdateTime: timestamp,
	}

	statefulset := &appsv1.StatefulSet{}
	if err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, statefulset); err != nil {
		if apierrors.IsNotFound(err) {
			status.State = "Not Found"
			status.Error = "StatefulSet does not exist"
		} else {
			status.State = "Error"
			status.Error = fmt.Sprintf("Failed to get statefulset: %v", err)
		}
		return status
	}

	// Check if scaled down
	if statefulset.Spec.Replicas != nil && *statefulset.Spec.Replicas == 0 {
		status.State = "Scaled Down"
	} else {
		status.State = "Active"
		status.Error = fmt.Sprintf("StatefulSet has %d replicas", *statefulset.Spec.Replicas)
	}

	return status
}

// getCronJobStatus gets the status of a cronjob
func (m *Manager) getCronJobStatus(ctx context.Context, name, namespace string) crdv1alpha1.WorkloadStatus {
	timestamp := time.Now().Format(time.RFC3339)
	status := crdv1alpha1.WorkloadStatus{
		Name:           name,
		Kind:           "CronJob",
		LastUpdateTime: timestamp,
	}

	cronjob := &batchv1.CronJob{}
	if err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, cronjob); err != nil {
		if apierrors.IsNotFound(err) {
			status.State = "Not Found"
			status.Error = "CronJob does not exist"
		} else {
			status.State = "Error"
			status.Error = fmt.Sprintf("Failed to get cronjob: %v", err)
		}
		return status
	}

	// Check if suspended
	if cronjob.Spec.Suspend != nil && *cronjob.Spec.Suspend {
		status.State = "Suspended"
	} else {
		status.State = "Active"
		status.Error = "CronJob is not suspended"
	}

	return status
}
