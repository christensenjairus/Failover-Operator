package workload

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	crdv1alpha1 "github.com/christensenjairus/Failover-Operator/api/v1alpha1"
)

// Manager handles operations related to workload resources
type Manager struct {
	client client.Client
}

// NewManager creates a new Workload manager
func NewManager(client client.Client) *Manager {
	return &Manager{
		client: client,
	}
}

// ProcessWorkloads handles scaling deployments, statefulsets, and suspending cronjobs based on the desired state
func (m *Manager) ProcessWorkloads(ctx context.Context, namespace string, deployments, statefulSets, cronJobs []string, desiredState string) error {
	log := log.FromContext(ctx)

	// If we're in primary mode, we don't need to scale anything
	// Flux will handle scaling up workloads in primary mode
	if desiredState == "primary" {
		log.Info("Primary mode - no workload changes needed")
		return nil
	}

	// In secondary mode, we need to ensure all workloads are scaled down/suspended
	log.Info("Secondary mode - ensuring workloads are scaled down/suspended",
		"Deployments", len(deployments),
		"StatefulSets", len(statefulSets),
		"CronJobs", len(cronJobs))

	// First check if any workload is not in the desired state
	statuses := m.GetWorkloadStatuses(ctx, namespace, deployments, statefulSets, cronJobs)
	anyWorkloadActive := false

	// Check deployments
	for _, status := range statuses {
		if (status.Kind == "Deployment" || status.Kind == "StatefulSet") && status.State != "Scaled Down" {
			log.Info("Found active workload in secondary mode", "Kind", status.Kind, "Name", status.Name, "State", status.State)
			anyWorkloadActive = true
		} else if status.Kind == "CronJob" && status.State != "Suspended" {
			log.Info("Found active CronJob in secondary mode", "Name", status.Name, "State", status.State)
			anyWorkloadActive = true
		}
	}

	if !anyWorkloadActive {
		log.Info("All workloads are already in the correct state")
	} else {
		log.Info("Some workloads need to be scaled down/suspended - applying changes now")
	}

	// Process Deployments - scale down to 0
	for _, name := range deployments {
		if err := m.ensureDeploymentScaledDown(ctx, namespace, name); err != nil {
			log.Error(err, "Failed to scale Deployment", "Deployment", name)
			return err
		}
	}

	// Process StatefulSets - scale down to 0
	for _, name := range statefulSets {
		if err := m.ensureStatefulSetScaledDown(ctx, namespace, name); err != nil {
			log.Error(err, "Failed to scale StatefulSet", "StatefulSet", name)
			return err
		}
	}

	// Process CronJobs - suspend
	for _, name := range cronJobs {
		if err := m.ensureCronJobSuspended(ctx, namespace, name); err != nil {
			log.Error(err, "Failed to suspend CronJob", "CronJob", name)
			return err
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

// ensureDeploymentScaledDown ensures a Deployment is scaled to 0 replicas
func (m *Manager) ensureDeploymentScaledDown(ctx context.Context, namespace, name string) error {
	return m.scaleDeployment(ctx, namespace, name, 0)
}

// ensureStatefulSetScaledDown ensures a StatefulSet is scaled to 0 replicas
func (m *Manager) ensureStatefulSetScaledDown(ctx context.Context, namespace, name string) error {
	return m.scaleStatefulSet(ctx, namespace, name, 0)
}

// ensureCronJobSuspended ensures a CronJob is suspended
func (m *Manager) ensureCronJobSuspended(ctx context.Context, namespace, name string) error {
	return m.suspendCronJob(ctx, namespace, name, true)
}

// GetWorkloadStatuses retrieves the current status of all managed workloads
func (m *Manager) GetWorkloadStatuses(ctx context.Context, namespace string, deployments, statefulSets, cronJobs []string) []crdv1alpha1.WorkloadStatus {
	var statuses []crdv1alpha1.WorkloadStatus
	timestamp := time.Now().Format(time.RFC3339)

	// Check deployments
	for _, name := range deployments {
		status := m.getDeploymentStatus(ctx, namespace, name)
		status.LastUpdateTime = timestamp
		statuses = append(statuses, status)
	}

	// Check statefulsets
	for _, name := range statefulSets {
		status := m.getStatefulSetStatus(ctx, namespace, name)
		status.LastUpdateTime = timestamp
		statuses = append(statuses, status)
	}

	// Check cronjobs
	for _, name := range cronJobs {
		status := m.getCronJobStatus(ctx, namespace, name)
		status.LastUpdateTime = timestamp
		statuses = append(statuses, status)
	}

	return statuses
}

// getDeploymentStatus gets the current status of a Deployment
func (m *Manager) getDeploymentStatus(ctx context.Context, namespace, name string) crdv1alpha1.WorkloadStatus {
	log := log.FromContext(ctx)
	status := crdv1alpha1.WorkloadStatus{
		Name: name,
		Kind: "Deployment",
	}

	deployment := &appsv1.Deployment{}
	err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, deployment)
	if err != nil {
		log.Error(err, "Failed to get Deployment status", "Deployment", name)
		status.Error = fmt.Sprintf("Failed to get status: %v", err)
		status.State = "Unknown"
		return status
	}

	// Check if scaled down
	if deployment.Spec.Replicas != nil && *deployment.Spec.Replicas == 0 {
		status.State = "Scaled Down"
	} else {
		replicas := 0
		if deployment.Spec.Replicas != nil {
			replicas = int(*deployment.Spec.Replicas)
		}
		status.State = fmt.Sprintf("Active (%d replicas)", replicas)
	}

	return status
}

// getStatefulSetStatus gets the current status of a StatefulSet
func (m *Manager) getStatefulSetStatus(ctx context.Context, namespace, name string) crdv1alpha1.WorkloadStatus {
	log := log.FromContext(ctx)
	status := crdv1alpha1.WorkloadStatus{
		Name: name,
		Kind: "StatefulSet",
	}

	statefulSet := &appsv1.StatefulSet{}
	err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, statefulSet)
	if err != nil {
		log.Error(err, "Failed to get StatefulSet status", "StatefulSet", name)
		status.Error = fmt.Sprintf("Failed to get status: %v", err)
		status.State = "Unknown"
		return status
	}

	// Check if scaled down
	if statefulSet.Spec.Replicas != nil && *statefulSet.Spec.Replicas == 0 {
		status.State = "Scaled Down"
	} else {
		replicas := 0
		if statefulSet.Spec.Replicas != nil {
			replicas = int(*statefulSet.Spec.Replicas)
		}
		status.State = fmt.Sprintf("Active (%d replicas)", replicas)
	}

	return status
}

// getCronJobStatus gets the current status of a CronJob
func (m *Manager) getCronJobStatus(ctx context.Context, namespace, name string) crdv1alpha1.WorkloadStatus {
	log := log.FromContext(ctx)
	status := crdv1alpha1.WorkloadStatus{
		Name: name,
		Kind: "CronJob",
	}

	cronJob := &batchv1.CronJob{}
	err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, cronJob)
	if err != nil {
		log.Error(err, "Failed to get CronJob status", "CronJob", name)
		status.Error = fmt.Sprintf("Failed to get status: %v", err)
		status.State = "Unknown"
		return status
	}

	// Check if suspended
	if cronJob.Spec.Suspend != nil && *cronJob.Spec.Suspend {
		status.State = "Suspended"
	} else {
		status.State = "Active"
	}

	return status
}
