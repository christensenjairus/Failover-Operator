package workload

import (
	"context"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	crdv1alpha1 "github.com/christensenjairus/Failover-Operator/api/v1alpha1"
)

// Manager is responsible for managing workloads
type Manager struct {
	client client.Client
}

// NewManager creates a new workload manager
func NewManager(c client.Client) *Manager {
	return &Manager{client: c}
}

// ProcessWorkloads processes workloads based on the desired state
func ProcessWorkloads(ctx context.Context, mgr *Manager, deployments, statefulSets, cronJobs []crdv1alpha1.ResourceRef, desiredState string, policyNamespace string) error {
	log := log.FromContext(ctx)

	// Normalize the desired state for consistent handling
	normalizedState := strings.ToUpper(desiredState)
	if strings.EqualFold(desiredState, "active") || strings.EqualFold(desiredState, "primary") {
		normalizedState = "PRIMARY"
	} else if strings.EqualFold(desiredState, "passive") || strings.EqualFold(desiredState, "secondary") || strings.EqualFold(desiredState, "standby") {
		normalizedState = "STANDBY"
	}

	log.Info("Processing workloads",
		"originalState", desiredState,
		"normalizedState", normalizedState,
		"deployments", len(deployments),
		"statefulSets", len(statefulSets),
		"cronJobs", len(cronJobs))

	// Process deployments and statefulsets
	if len(deployments) > 0 || len(statefulSets) > 0 {
		// In PRIMARY mode, Flux will handle restoring workloads
		if normalizedState == "PRIMARY" {
			log.Info("PRIMARY mode: Flux will handle restoring deployments and statefulsets")
		} else {
			log.Info("STANDBY mode: Scaling down deployments and statefulsets")

			if err := ProcessDeploymentsAndStatefulSets(ctx, mgr, deployments, statefulSets, normalizedState, policyNamespace); err != nil {
				return err
			}
		}
	}

	// Process cronjobs if any are defined
	if len(cronJobs) > 0 {
		log.Info("Processing cronjobs", "count", len(cronJobs), "mode", normalizedState)
		if err := ProcessCronJobs(ctx, mgr, cronJobs, normalizedState, policyNamespace); err != nil {
			return err
		}
	}

	return nil
}

// ProcessCronJobs processes CronJobs based on the desired state
func ProcessCronJobs(ctx context.Context, mgr *Manager, cronJobs []crdv1alpha1.ResourceRef, desiredState string, policyNamespace string) error {
	log := log.FromContext(ctx)

	// Normalize the state if needed
	normalizedState := strings.ToUpper(desiredState)
	if strings.EqualFold(desiredState, "active") || strings.EqualFold(desiredState, "primary") {
		normalizedState = "PRIMARY"
	} else if strings.EqualFold(desiredState, "passive") || strings.EqualFold(desiredState, "secondary") || strings.EqualFold(desiredState, "standby") {
		normalizedState = "STANDBY"
	}

	// Determine if we should suspend or resume
	suspend := normalizedState == "STANDBY"

	log.Info("Processing cronjobs",
		"count", len(cronJobs),
		"suspend", suspend,
		"normalizedState", normalizedState)

	// Process all cronjobs
	for _, cj := range cronJobs {
		// Use policy namespace for all resources
		ns := policyNamespace

		// Get the CronJob
		cronJob := &batchv1.CronJob{}
		err := mgr.client.Get(ctx, types.NamespacedName{Name: cj.Name, Namespace: ns}, cronJob)
		if err != nil {
			if apierrors.IsNotFound(err) {
				// If not found, log and continue
				log.Info("CronJob not found, skipping", "name", cj.Name, "namespace", ns)
				continue
			}
			log.Error(err, "Failed to get CronJob", "name", cj.Name, "namespace", ns)
			return err
		}

		// Check if the cronjob needs to be suspended or resumed
		if cronJob.Spec.Suspend == nil || *cronJob.Spec.Suspend != suspend {
			// Update the suspend field
			log.Info("Updating CronJob", "name", cj.Name, "namespace", ns, "suspend", suspend)
			cronJob.Spec.Suspend = &suspend
			if err := mgr.client.Update(ctx, cronJob); err != nil {
				log.Error(err, "Failed to update CronJob", "name", cj.Name, "namespace", ns)
				return err
			}
		} else {
			log.Info("CronJob already in correct state", "name", cj.Name, "namespace", ns, "suspend", suspend)
		}
	}

	return nil
}

// ProcessDeploymentsAndStatefulSets processes Deployments and StatefulSets
func ProcessDeploymentsAndStatefulSets(ctx context.Context, mgr *Manager, deployments, statefulSets []crdv1alpha1.ResourceRef, desiredState string, policyNamespace string) error {
	log := log.FromContext(ctx)

	// Normalize the desired state for consistency
	normalizedState := strings.ToUpper(desiredState)
	if strings.EqualFold(desiredState, "active") || strings.EqualFold(desiredState, "primary") {
		normalizedState = "PRIMARY"
	} else if strings.EqualFold(desiredState, "passive") || strings.EqualFold(desiredState, "secondary") || strings.EqualFold(desiredState, "standby") {
		normalizedState = "STANDBY"
	}

	// In primary mode, Flux will handle scaling up deployments and stateful sets
	if normalizedState == "PRIMARY" {
		log.Info("PRIMARY mode: Flux will handle restoring deployments and statefulsets")
		return nil
	}

	// In STANDBY mode, scale down to 0
	log.Info("STANDBY mode: Scaling down workloads",
		"deployments", len(deployments),
		"statefulSets", len(statefulSets))

	// Process all deployments
	for _, deployment := range deployments {
		// Use policy namespace for all resources
		ns := policyNamespace

		// Get the Deployment
		dep := &appsv1.Deployment{}
		err := mgr.client.Get(ctx, types.NamespacedName{Name: deployment.Name, Namespace: ns}, dep)
		if err != nil {
			if apierrors.IsNotFound(err) {
				// If not found, log and continue
				log.Info("Deployment not found, skipping", "name", deployment.Name, "namespace", ns)
				continue
			}
			log.Error(err, "Failed to get Deployment", "name", deployment.Name, "namespace", ns)
			return err
		}

		// Scale down to 0 replicas if not already at 0
		if dep.Spec.Replicas == nil || *dep.Spec.Replicas > 0 {
			log.Info("Scaling down Deployment", "name", deployment.Name, "namespace", ns, "currentReplicas", *dep.Spec.Replicas)
			zeroReplicas := int32(0)
			dep.Spec.Replicas = &zeroReplicas
			if err := mgr.client.Update(ctx, dep); err != nil {
				log.Error(err, "Failed to scale down Deployment", "name", deployment.Name, "namespace", ns)
				return err
			}
		} else {
			log.Info("Deployment already scaled to 0", "name", deployment.Name, "namespace", ns)
		}
	}

	// Process all statefulsets
	for _, statefulSet := range statefulSets {
		// Use policy namespace for all resources
		ns := policyNamespace

		// Get the StatefulSet
		sts := &appsv1.StatefulSet{}
		err := mgr.client.Get(ctx, types.NamespacedName{Name: statefulSet.Name, Namespace: ns}, sts)
		if err != nil {
			if apierrors.IsNotFound(err) {
				// If not found, log and continue
				log.Info("StatefulSet not found, skipping", "name", statefulSet.Name, "namespace", ns)
				continue
			}
			log.Error(err, "Failed to get StatefulSet", "name", statefulSet.Name, "namespace", ns)
			return err
		}

		// Scale down to 0 replicas if not already at 0
		if sts.Spec.Replicas == nil || *sts.Spec.Replicas > 0 {
			log.Info("Scaling down StatefulSet", "name", statefulSet.Name, "namespace", ns, "currentReplicas", *sts.Spec.Replicas)
			zeroReplicas := int32(0)
			sts.Spec.Replicas = &zeroReplicas
			if err := mgr.client.Update(ctx, sts); err != nil {
				log.Error(err, "Failed to scale down StatefulSet", "name", statefulSet.Name, "namespace", ns)
				return err
			}
		} else {
			log.Info("StatefulSet already scaled to 0", "name", statefulSet.Name, "namespace", ns)
		}
	}

	return nil
}
