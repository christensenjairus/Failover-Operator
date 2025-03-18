package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	crdv1alpha1 "github.com/christensenjairus/Failover-Operator/api/v1alpha1"
	"github.com/christensenjairus/Failover-Operator/internal/controller/flux"
	"github.com/christensenjairus/Failover-Operator/internal/controller/status"
	"github.com/christensenjairus/Failover-Operator/internal/controller/virtualservice"
	"github.com/christensenjairus/Failover-Operator/internal/controller/volumereplication"
	"github.com/christensenjairus/Failover-Operator/internal/controller/workload"
	replicationv1alpha1 "github.com/csi-addons/kubernetes-csi-addons/api/replication.storage/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Deprecated: Use RegisterSchemes from setup.go instead
func init() {
	// This function is kept for backwards compatibility
	// Please use RegisterSchemes from setup.go instead
	replicationv1alpha1.SchemeBuilder.Register(&replicationv1alpha1.VolumeReplication{}, &replicationv1alpha1.VolumeReplicationList{})
}

// FailoverPolicyReconciler reconciles a FailoverPolicy object
type FailoverPolicyReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=crd.hahomelabs.com,resources=failoverpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=crd.hahomelabs.com,resources=failoverpolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=crd.hahomelabs.com,resources=failoverpolicies/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=batch,resources=cronjobs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=apps,resources=virtualservices,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=helm.toolkit.fluxcd.io,resources=helmreleases,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=kustomize.toolkit.fluxcd.io,resources=kustomizations,verbs=get;list;watch;update;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *FailoverPolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Fetch the FailoverPolicy instance
	failoverPolicy := &crdv1alpha1.FailoverPolicy{}
	err := r.Get(ctx, req.NamespacedName, failoverPolicy)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found - could have been deleted after reconcile request.
			// Return and don't requeue
			log.Info("FailoverPolicy resource not found. Ignoring since object must be deleted.")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		log.Error(err, "Failed to get FailoverPolicy")
		return ctrl.Result{}, err
	}

	// Create managers
	vrManager := volumereplication.NewManager(r.Client)
	workloadManager := workload.NewManager(r.Client)
	fluxManager := flux.NewManager(r.Client)
	vsManager := virtualservice.NewManager(r.Client)
	statusManager := status.NewManager(r.Client, vrManager)

	// Group resources by type
	resourcesByType := groupResourcesByType(failoverPolicy)

	// Get a list of VolumeReplication names
	vrNames := make([]string, 0, len(resourcesByType.volumeReplications))
	for _, vr := range resourcesByType.volumeReplications {
		vrNames = append(vrNames, vr.Name)
	}

	// Check if we're in safe mode - defaults to "on" if not specified
	isSafeMode := true
	if failoverPolicy.Spec.Mode != "" {
		isSafeMode = strings.ToLower(failoverPolicy.Spec.Mode) != "off"
	}

	// In unsafe mode, process all resources in parallel without waiting
	if !isSafeMode {
		log.Info("Operating in unsafe mode - bypassing ordered sequencing")

		// Process all resources in parallel without waiting

		// 1. Process Flux resources
		if len(resourcesByType.helmReleases) > 0 || len(resourcesByType.kustomizations) > 0 {
			log.Info("Processing Flux resources", "desiredState", failoverPolicy.Spec.DesiredState)
			if err := fluxManager.ProcessFluxResources(ctx,
				resourcesByType.helmReleases,
				resourcesByType.kustomizations,
				failoverPolicy.Namespace,
				failoverPolicy.Spec.DesiredState); err != nil {
				log.Error(err, "Failed to process Flux resources")
			}
		}

		// 2. Process VirtualServices
		if len(resourcesByType.virtualServices) > 0 {
			vsNames := getResourceNames(resourcesByType.virtualServices)
			log.Info("Processing VirtualServices", "count", len(vsNames))
			vsManager.ProcessVirtualServices(ctx, failoverPolicy.Namespace, vsNames, failoverPolicy.Spec.DesiredState)
		}

		// 3. Process all workloads
		deploymentNames := getResourceNames(resourcesByType.deployments)
		statefulSetNames := getResourceNames(resourcesByType.statefulSets)
		cronJobNames := getResourceNames(resourcesByType.cronJobs)

		if len(deploymentNames) > 0 || len(statefulSetNames) > 0 || len(cronJobNames) > 0 {
			err = workloadManager.ProcessWorkloads(ctx,
				deploymentNames,
				statefulSetNames,
				cronJobNames,
				failoverPolicy.Namespace,
				failoverPolicy.Spec.DesiredState)
			if err != nil {
				log.Error(err, "Failed to process workloads")
			}
		}

		// 4. Process VolumeReplications
		log.Info("Processing VolumeReplications", "count", len(vrNames))
		failoverError, failoverErrorMessage := vrManager.ProcessVolumeReplications(ctx, failoverPolicy.Namespace, vrNames, failoverPolicy.Spec.DesiredState, failoverPolicy.Spec.Mode)
		if failoverError {
			log.Error(nil, "Failed to process volume replications", "error", failoverErrorMessage)
			statusErr := statusManager.UpdateFailoverStatus(ctx, failoverPolicy, failoverError, failoverErrorMessage)
			if statusErr != nil {
				log.Error(statusErr, "Failed to update status after volume replication error")
			}
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}

		// Update status
		if err := statusManager.UpdateStatus(ctx, failoverPolicy); err != nil {
			log.Error(err, "Failed to update status")
			return ctrl.Result{}, err
		}

		// Update status to indicate successful reconciliation
		statusErr := statusManager.UpdateFailoverStatus(ctx, failoverPolicy, false, "")
		if statusErr != nil {
			log.Error(statusErr, "Failed to update status after successful reconciliation")
			return ctrl.Result{}, statusErr
		}

		// Requeue more frequently in unsafe mode to compensate for lack of ordered processing
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	// Continue with safe mode processing using the ordered sequencing
	if failoverPolicy.Spec.DesiredState == "secondary" {
		// For secondary mode: Flux first, then VirtualServices/CronJobs, then other workloads, then VolumeReplications

		// 1. First suspend Flux resources if any are defined (first to suspend)
		if len(resourcesByType.helmReleases) > 0 || len(resourcesByType.kustomizations) > 0 {
			log.Info("Suspending Flux resources first")
			if err := fluxManager.ProcessFluxResources(ctx,
				resourcesByType.helmReleases,
				resourcesByType.kustomizations,
				failoverPolicy.Namespace,
				failoverPolicy.Spec.DesiredState); err != nil {
				log.Error(err, "Failed to process Flux resources")
				statusErr := statusManager.UpdateFailoverStatus(ctx, failoverPolicy, true, fmt.Sprintf("Failed to process Flux resources: %v", err))
				if statusErr != nil {
					log.Error(statusErr, "Failed to update status after Flux error")
				}
				return ctrl.Result{}, err
			}
		}

		// 2. Immediately process VirtualServices (these need to change quickly)
		if len(resourcesByType.virtualServices) > 0 {
			vsNames := getResourceNames(resourcesByType.virtualServices)
			log.Info("Processing VirtualServices immediately", "count", len(vsNames))
			vsManager.ProcessVirtualServices(ctx, failoverPolicy.Namespace, vsNames, failoverPolicy.Spec.DesiredState)
		}

		// 3. Process CronJobs immediately (these can be handled quickly)
		cronJobNames := getResourceNames(resourcesByType.cronJobs)
		if len(cronJobNames) > 0 {
			log.Info("Processing CronJobs immediately", "count", len(cronJobNames))
			err = workloadManager.ProcessCronJobs(ctx, cronJobNames, failoverPolicy.Namespace, failoverPolicy.Spec.DesiredState)
			if err != nil {
				log.Error(err, "Failed to process CronJobs")
				statusErr := statusManager.UpdateFailoverStatus(ctx, failoverPolicy, true, fmt.Sprintf("Failed to process CronJobs: %v", err))
				if statusErr != nil {
					log.Error(statusErr, "Failed to update status after CronJob error")
				}
				return ctrl.Result{}, err
			}
		}

		// 4. Process Deployments and StatefulSets
		deploymentNames := getResourceNames(resourcesByType.deployments)
		statefulSetNames := getResourceNames(resourcesByType.statefulSets)
		if len(deploymentNames) > 0 || len(statefulSetNames) > 0 {
			log.Info("Processing Deployments and StatefulSets", "deployments", len(deploymentNames), "statefulSets", len(statefulSetNames))
			err = workloadManager.ProcessDeploymentsAndStatefulSets(ctx, deploymentNames, statefulSetNames, failoverPolicy.Namespace, failoverPolicy.Spec.DesiredState)
			if err != nil {
				log.Error(err, "Failed to process Deployments and StatefulSets")
				statusErr := statusManager.UpdateFailoverStatus(ctx, failoverPolicy, true, fmt.Sprintf("Failed to process workloads: %v", err))
				if statusErr != nil {
					log.Error(statusErr, "Failed to update status after workload error")
				}
				return ctrl.Result{}, err
			}
		}

		// 5. Check if all workloads are fully scaled down before proceeding with VolumeReplications
		allWorkloadsReady := true

		// Update status to get current workload states
		if err := statusManager.UpdateStatus(ctx, failoverPolicy); err != nil {
			log.Error(err, "Failed to update status")
			return ctrl.Result{RequeueAfter: 5 * time.Second}, err
		}

		// Check workload statuses
		log.Info("Checking if all workloads are fully scaled down before processing VolumeReplications")
		if len(failoverPolicy.Status.WorkloadStatuses) > 0 {
			for _, status := range failoverPolicy.Status.WorkloadStatuses {
				if status.Kind == "Deployment" || status.Kind == "StatefulSet" {
					if status.State == "Scaling Down" {
						log.Info("Workload is still scaling down",
							"Kind", status.Kind,
							"Name", status.Name,
							"CurrentState", status.State,
							"Details", status.Error)
						allWorkloadsReady = false
					} else if status.State != "Scaled Down" && status.State != "Not Found" {
						log.Info("Workload is not scaled down",
							"Kind", status.Kind,
							"Name", status.Name,
							"CurrentState", status.State)
						allWorkloadsReady = false
					} else {
						log.Info("Workload is fully scaled down",
							"Kind", status.Kind,
							"Name", status.Name,
							"CurrentState", status.State)
					}
				} else if status.Kind == "CronJob" && status.State != "Suspended" {
					log.Info("Waiting for CronJob to be suspended",
						"Name", status.Name,
						"CurrentState", status.State)
					allWorkloadsReady = false
				} else if (status.Kind == "HelmRelease" || status.Kind == "Kustomization") && status.State != "Suspended" {
					log.Info("Waiting for Flux resource to be suspended",
						"Kind", status.Kind,
						"Name", status.Name,
						"CurrentState", status.State)
					allWorkloadsReady = false
				}
			}
		}

		// If workloads are not ready yet, requeue and wait
		if !allWorkloadsReady {
			log.Info("Some workloads are still shutting down, waiting before processing VolumeReplications")
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}

		log.Info("All workloads are fully scaled down and terminated")

		// 6. Once workloads are scaled down, process VolumeReplications last
		log.Info("All workloads are fully terminated, now processing VolumeReplications",
			"count", len(vrNames),
			"names", vrNames)
		failoverError, failoverErrorMessage := vrManager.ProcessVolumeReplications(ctx, failoverPolicy.Namespace, vrNames, failoverPolicy.Spec.DesiredState, failoverPolicy.Spec.Mode)
		if failoverError {
			log.Error(nil, "Failed to process volume replications", "error", failoverErrorMessage)
			statusErr := statusManager.UpdateFailoverStatus(ctx, failoverPolicy, failoverError, failoverErrorMessage)
			if statusErr != nil {
				log.Error(statusErr, "Failed to update status after volume replication error")
			}
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}
	} else {
		// For primary mode: VR first, then VirtualServices/CronJobs, then wait for VR to be fully primary, then Flux last

		// 1. Process VolumeReplications first
		log.Info("Processing VolumeReplications first")
		failoverError, failoverErrorMessage := vrManager.ProcessVolumeReplications(ctx, failoverPolicy.Namespace, vrNames, failoverPolicy.Spec.DesiredState, failoverPolicy.Spec.Mode)
		if failoverError {
			log.Error(nil, "Failed to process volume replications", "error", failoverErrorMessage)
			statusErr := statusManager.UpdateFailoverStatus(ctx, failoverPolicy, failoverError, failoverErrorMessage)
			if statusErr != nil {
				log.Error(statusErr, "Failed to update status after volume replication error")
			}
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}

		// 2. Process VirtualServices immediately
		if len(resourcesByType.virtualServices) > 0 {
			vsNames := getResourceNames(resourcesByType.virtualServices)
			log.Info("Processing VirtualServices immediately", "count", len(vsNames))
			vsManager.ProcessVirtualServices(ctx, failoverPolicy.Namespace, vsNames, failoverPolicy.Spec.DesiredState)
		}

		// 3. Process CronJobs immediately
		cronJobNames := getResourceNames(resourcesByType.cronJobs)
		if len(cronJobNames) > 0 {
			log.Info("Processing CronJobs immediately", "count", len(cronJobNames))
			err = workloadManager.ProcessCronJobs(ctx, cronJobNames, failoverPolicy.Namespace, failoverPolicy.Spec.DesiredState)
			if err != nil {
				log.Error(err, "Failed to process CronJobs")
				statusErr := statusManager.UpdateFailoverStatus(ctx, failoverPolicy, true, fmt.Sprintf("Failed to process CronJobs: %v", err))
				if statusErr != nil {
					log.Error(statusErr, "Failed to update status after CronJob error")
				}
				return ctrl.Result{}, err
			}
		}

		// 4. Wait for all VolumeReplications to reach PRIMARY state before resuming Flux
		if len(vrNames) > 0 && (len(resourcesByType.helmReleases) > 0 || len(resourcesByType.kustomizations) > 0) {
			// Update status to get current VR states
			if err := statusManager.UpdateStatus(ctx, failoverPolicy); err != nil {
				log.Error(err, "Failed to update status")
				return ctrl.Result{RequeueAfter: 5 * time.Second}, err
			}

			log.Info("Checking if all VolumeReplications have reached PRIMARY state before resuming Flux")
			allVolumesReady, message := vrManager.AreAllVolumesInDesiredState(ctx, failoverPolicy.Namespace, vrNames, failoverPolicy.Spec.DesiredState)

			if !allVolumesReady {
				log.Info("Waiting for VolumeReplications to reach PRIMARY state before resuming Flux", "message", message)
				return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
			}

			log.Info("All VolumeReplications have reached PRIMARY state, proceeding to resume Flux")
		}

		// 5. Process Flux resources last (Flux will handle scaling up the workloads)
		if len(resourcesByType.helmReleases) > 0 || len(resourcesByType.kustomizations) > 0 {
			log.Info("Resuming Flux resources last (Flux will handle scaling up workloads)")
			if err := fluxManager.ProcessFluxResources(ctx,
				resourcesByType.helmReleases,
				resourcesByType.kustomizations,
				failoverPolicy.Namespace,
				failoverPolicy.Spec.DesiredState); err != nil {
				log.Error(err, "Failed to process Flux resources")
				statusErr := statusManager.UpdateFailoverStatus(ctx, failoverPolicy, true, fmt.Sprintf("Failed to process Flux resources: %v", err))
				if statusErr != nil {
					log.Error(statusErr, "Failed to update status after Flux error")
				}
				return ctrl.Result{}, err
			}
		}
	}

	// Update status
	if err := statusManager.UpdateStatus(ctx, failoverPolicy); err != nil {
		log.Error(err, "Failed to update status")
		return ctrl.Result{}, err
	}

	// Update status to indicate successful reconciliation
	statusErr := statusManager.UpdateFailoverStatus(ctx, failoverPolicy, false, "")
	if statusErr != nil {
		log.Error(statusErr, "Failed to update status after successful reconciliation")
		return ctrl.Result{}, statusErr
	}

	// If in secondary mode, requeue periodically to ensure workloads stay scaled down
	if failoverPolicy.Spec.DesiredState == "secondary" {
		// Check if any workload is not in the desired state
		needsRequeueSoon := false
		if len(failoverPolicy.Status.WorkloadStatuses) > 0 {
			for _, status := range failoverPolicy.Status.WorkloadStatuses {
				if (status.Kind == "Deployment" || status.Kind == "StatefulSet") && status.State != "Scaled Down" {
					needsRequeueSoon = true
					break
				} else if status.Kind == "CronJob" && status.State != "Suspended" {
					needsRequeueSoon = true
					break
				} else if (status.Kind == "HelmRelease" || status.Kind == "Kustomization") && status.State != "Suspended" {
					needsRequeueSoon = true
					break
				}
			}
		}

		requeueAfter := 20 * time.Second // Changed from 60s to 20s for better responsiveness
		if needsRequeueSoon {
			requeueAfter = 5 * time.Second
			log.Info("Detected resources not in proper state, requeueing soon", "RequeueAfter", requeueAfter)
		}

		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}

	log.Info("Reconciliation completed",
		"Name", failoverPolicy.Name,
		"Namespace", failoverPolicy.Namespace,
		"DesiredState", failoverPolicy.Spec.DesiredState)
	return ctrl.Result{}, nil
}

// ResourcesByType holds references to resources grouped by their type
type ResourcesByType struct {
	volumeReplications []crdv1alpha1.ResourceReference
	virtualServices    []crdv1alpha1.ResourceReference
	deployments        []crdv1alpha1.ResourceReference
	statefulSets       []crdv1alpha1.ResourceReference
	cronJobs           []crdv1alpha1.ResourceReference
	helmReleases       []crdv1alpha1.ResourceReference
	kustomizations     []crdv1alpha1.ResourceReference
}

// groupResourcesByType organizes resources from managedResources by their type
// It also handles legacy resource references for backward compatibility
func groupResourcesByType(policy *crdv1alpha1.FailoverPolicy) ResourcesByType {
	result := ResourcesByType{
		volumeReplications: make([]crdv1alpha1.ResourceReference, 0),
		virtualServices:    make([]crdv1alpha1.ResourceReference, 0),
		deployments:        make([]crdv1alpha1.ResourceReference, 0),
		statefulSets:       make([]crdv1alpha1.ResourceReference, 0),
		cronJobs:           make([]crdv1alpha1.ResourceReference, 0),
		helmReleases:       make([]crdv1alpha1.ResourceReference, 0),
		kustomizations:     make([]crdv1alpha1.ResourceReference, 0),
	}

	// Add resources from managedResources field
	for _, resource := range policy.Spec.ManagedResources {
		ref := crdv1alpha1.ResourceReference{
			Name:      resource.Name,
			Namespace: resource.Namespace,
		}

		switch resource.Kind {
		case "VolumeReplication":
			result.volumeReplications = append(result.volumeReplications, ref)
		case "VirtualService":
			result.virtualServices = append(result.virtualServices, ref)
		case "Deployment":
			result.deployments = append(result.deployments, ref)
		case "StatefulSet":
			result.statefulSets = append(result.statefulSets, ref)
		case "CronJob":
			result.cronJobs = append(result.cronJobs, ref)
		case "HelmRelease":
			result.helmReleases = append(result.helmReleases, ref)
		case "Kustomization":
			result.kustomizations = append(result.kustomizations, ref)
		}
	}

	// Handle legacy resource references for backward compatibility
	for _, vr := range policy.Spec.VolumeReplications {
		result.volumeReplications = append(result.volumeReplications, vr)
	}
	for _, vs := range policy.Spec.VirtualServices {
		result.virtualServices = append(result.virtualServices, vs)
	}
	for _, deploy := range policy.Spec.Deployments {
		result.deployments = append(result.deployments, deploy)
	}
	for _, sts := range policy.Spec.StatefulSets {
		result.statefulSets = append(result.statefulSets, sts)
	}
	for _, cj := range policy.Spec.CronJobs {
		result.cronJobs = append(result.cronJobs, cj)
	}
	for _, hr := range policy.Spec.HelmReleases {
		result.helmReleases = append(result.helmReleases, hr)
	}
	for _, k := range policy.Spec.Kustomizations {
		result.kustomizations = append(result.kustomizations, k)
	}

	return result
}

// getResourceNames extracts names from a slice of ResourceReference
func getResourceNames(refs []crdv1alpha1.ResourceReference) []string {
	names := make([]string, len(refs))
	for i, ref := range refs {
		names[i] = ref.Name
	}
	return names
}

// SetupWithManager sets up the controller with the Manager.
func (r *FailoverPolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := RegisterSchemes(mgr.GetScheme()); err != nil {
		return err
	}

	// Register Flux schemes if they're available
	// This allows the operator to work even without Flux CRDs installed
	_ = registerFluxSchemes(mgr.GetScheme())

	// Create a watch handler function for workload resources
	workloadMapFunc := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		// Find all FailoverPolicies in the same namespace
		failoverPolicies := &crdv1alpha1.FailoverPolicyList{}
		if err := r.List(context.Background(), failoverPolicies, client.InNamespace(obj.GetNamespace())); err != nil {
			return nil
		}

		var requests []reconcile.Request
		objName := obj.GetName()
		objKind := obj.GetObjectKind().GroupVersionKind().Kind

		for _, policy := range failoverPolicies.Items {
			shouldEnqueue := false

			// Check the new managedResources field first
			for _, resource := range policy.Spec.ManagedResources {
				if resource.Kind == objKind && resource.Name == objName {
					// Check namespace if specified
					if resource.Namespace != "" && resource.Namespace != obj.GetNamespace() {
						continue // Skip if namespace doesn't match
					}
					shouldEnqueue = true
					break
				}
			}

			// For backward compatibility, also check the legacy fields
			if !shouldEnqueue {
				switch objKind {
				case "Deployment":
					for _, ref := range policy.Spec.Deployments {
						if ref.Name == objName {
							shouldEnqueue = true
							break
						}
					}
				case "StatefulSet":
					for _, ref := range policy.Spec.StatefulSets {
						if ref.Name == objName {
							shouldEnqueue = true
							break
						}
					}
				case "CronJob":
					for _, ref := range policy.Spec.CronJobs {
						if ref.Name == objName {
							shouldEnqueue = true
							break
						}
					}
				}
			}

			if shouldEnqueue {
				requests = append(requests, reconcile.Request{
					NamespacedName: types.NamespacedName{
						Name:      policy.Name,
						Namespace: policy.Namespace,
					},
				})
			}
		}
		return requests
	})

	// Create a watch handler function for VolumeReplication resources
	vrMapFunc := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		// Find all FailoverPolicies in the same namespace
		failoverPolicies := &crdv1alpha1.FailoverPolicyList{}
		if err := r.List(context.Background(), failoverPolicies, client.InNamespace(obj.GetNamespace())); err != nil {
			return nil
		}

		var requests []reconcile.Request
		objName := obj.GetName()

		for _, policy := range failoverPolicies.Items {
			shouldEnqueue := false

			// Check managedResources field first
			for _, resource := range policy.Spec.ManagedResources {
				if resource.Kind == "VolumeReplication" && resource.Name == objName {
					// Check namespace if specified
					if resource.Namespace != "" && resource.Namespace != obj.GetNamespace() {
						continue // Skip if namespace doesn't match
					}
					shouldEnqueue = true
					break
				}
			}

			// For backward compatibility, check legacy field too
			if !shouldEnqueue {
				for _, vrRef := range policy.Spec.VolumeReplications {
					if vrRef.Name == objName {
						shouldEnqueue = true
						break
					}
				}
			}

			if shouldEnqueue {
				requests = append(requests, reconcile.Request{
					NamespacedName: types.NamespacedName{
						Name:      policy.Name,
						Namespace: policy.Namespace,
					},
				})
			}
		}
		return requests
	})

	// Create a watch handler function for VirtualService resources
	vsMapFunc := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		// Find all FailoverPolicies in the same namespace
		failoverPolicies := &crdv1alpha1.FailoverPolicyList{}
		if err := r.List(context.Background(), failoverPolicies, client.InNamespace(obj.GetNamespace())); err != nil {
			return nil
		}

		var requests []reconcile.Request
		objName := obj.GetName()

		for _, policy := range failoverPolicies.Items {
			shouldEnqueue := false

			// Check managedResources field first
			for _, resource := range policy.Spec.ManagedResources {
				if resource.Kind == "VirtualService" && resource.Name == objName {
					// Check namespace if specified
					if resource.Namespace != "" && resource.Namespace != obj.GetNamespace() {
						continue // Skip if namespace doesn't match
					}
					shouldEnqueue = true
					break
				}
			}

			// For backward compatibility, check legacy field too
			if !shouldEnqueue {
				for _, vsRef := range policy.Spec.VirtualServices {
					if vsRef.Name == objName {
						shouldEnqueue = true
						break
					}
				}
			}

			if shouldEnqueue {
				requests = append(requests, reconcile.Request{
					NamespacedName: types.NamespacedName{
						Name:      policy.Name,
						Namespace: policy.Namespace,
					},
				})
			}
		}
		return requests
	})

	return ctrl.NewControllerManagedBy(mgr).
		For(&crdv1alpha1.FailoverPolicy{}).
		Owns(&replicationv1alpha1.VolumeReplication{}).
		Watches(
			&replicationv1alpha1.VolumeReplication{},
			vrMapFunc,
		).
		Watches(
			&appsv1.Deployment{},
			workloadMapFunc,
		).
		Watches(
			&appsv1.StatefulSet{},
			workloadMapFunc,
		).
		Watches(
			&batchv1.CronJob{},
			workloadMapFunc,
		).
		// Add watch for VirtualService resources
		Watches(
			&unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "networking.istio.io/v1",
					"kind":       "VirtualService",
				},
			},
			vsMapFunc,
		).
		WithOptions(controller.Options{MaxConcurrentReconciles: 2}).
		Complete(r)
}

// registerFluxSchemes attempts to register Flux schemas if they exist in the cluster
func registerFluxSchemes(scheme *runtime.Scheme) error {
	// This function is a no-op now since we're using unstructured approach
	// The flux package uses unstructured.Unstructured to work with Flux resources
	// This avoids direct dependencies on Flux CRDs
	log.Log.Info("Using unstructured approach for Flux resources - no direct schema registration needed")
	return nil
}
