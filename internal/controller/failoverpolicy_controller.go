package controller

import (
	"context"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	crdv1alpha1 "github.com/christensenjairus/Failover-Operator/api/v1alpha1"
	flux "github.com/christensenjairus/Failover-Operator/internal/controller/flux"
	"github.com/christensenjairus/Failover-Operator/internal/controller/status"
	"github.com/christensenjairus/Failover-Operator/internal/controller/virtualservice"
	"github.com/christensenjairus/Failover-Operator/internal/controller/volumereplication"
	workload "github.com/christensenjairus/Failover-Operator/internal/controller/workload"
	replicationv1alpha1 "github.com/csi-addons/kubernetes-csi-addons/api/replication.storage/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Deprecated: Use RegisterSchemes from setup.go instead
func init() {
	// This function is kept for backwards compatibility
	// Please use RegisterSchemes from setup.go instead
	replicationv1alpha1.SchemeBuilder.Register(&replicationv1alpha1.VolumeReplication{}, &replicationv1alpha1.VolumeReplicationList{})
}

// Constants for reconciliation intervals
const (
	DefaultActiveReconcileInterval  = 5 * time.Minute
	DefaultPassiveReconcileInterval = 20 * time.Second
	HealthCheckInterval             = 30 * time.Second // Run health checks every 30 seconds
)

// ReconcileAnnotation defines the annotation to block reconciliation
const ReconcileAnnotation = "failover-operator.hahomelabs.com/block-reconcile"

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
// +kubebuilder:rbac:groups=networking.istio.io,resources=virtualservices,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=helm.toolkit.fluxcd.io,resources=helmreleases,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=kustomize.toolkit.fluxcd.io,resources=kustomizations,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=replication.storage.openshift.io,resources=volumereplications,verbs=get;list;watch;update;patch;create;delete
// +kubebuilder:rbac:groups=replication.storage.openshift.io,resources=volumereplications/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *FailoverPolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Get the FailoverPolicy instance
	failoverPolicy := &crdv1alpha1.FailoverPolicy{}
	if err := r.Get(ctx, req.NamespacedName, failoverPolicy); err != nil {
		// We'll ignore not-found errors, since there's nothing to reconcile.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Check if the request is for a health check
	isHealthCheck := false
	if val, exists := failoverPolicy.Annotations["failover-operator.hahomelabs.com/last-health-check"]; exists {
		lastCheckTime, err := time.Parse(time.RFC3339, val)
		if err == nil {
			timeSinceLastCheck := time.Since(lastCheckTime)
			if timeSinceLastCheck < HealthCheckInterval {
				// We recently did a health check, this is not a health check reconcile
				isHealthCheck = false
			} else {
				// It's been more than HealthCheckInterval since last check, this is a health check
				isHealthCheck = true
			}
		}
	} else {
		// No timestamp, this is our first health check
		isHealthCheck = true
	}

	// Extract manager instances we'll need for processing
	statusMgr := status.NewManager(r.Client, volumereplication.NewManager(r.Client))

	// Check if the reconciliation is just for health check
	if isHealthCheck {
		log.Info("Running periodic health check")

		// Update the health status
		if err := statusMgr.UpdateStatus(ctx, failoverPolicy); err != nil {
			log.Error(err, "Failed to update health status")
			return ctrl.Result{}, err
		}

		// Update the timestamp annotation
		if failoverPolicy.Annotations == nil {
			failoverPolicy.Annotations = make(map[string]string)
		}
		failoverPolicy.Annotations["failover-operator.hahomelabs.com/last-health-check"] = time.Now().Format(time.RFC3339)

		// Save the updated annotation
		if err := r.Update(ctx, failoverPolicy); err != nil {
			log.Error(err, "Failed to update health check timestamp")
			return ctrl.Result{}, err
		}

		// Set the health status
		if err := r.Status().Update(ctx, failoverPolicy); err != nil {
			log.Error(err, "Failed to update health status")
			return ctrl.Result{}, err
		}

		// Requeue after the health check interval
		return ctrl.Result{RequeueAfter: HealthCheckInterval}, nil
	}

	// Continue with normal reconciliation...

	// Check Volume Replications to make sure they exist in the namespace
	// This code provides early validation of the VolumeReplication resources
	// and helpful error messages in the operator logs for debugging
	volRepMgr := volumereplication.NewManager(r.Client)
	volReps := getVolumeReplications(failoverPolicy)

	if len(volReps) > 0 {
		log.Info("Checking VolumeReplication resources", "count", len(volReps), "namespace", failoverPolicy.Namespace)

		notFoundCount := 0
		for _, volRep := range volReps {
			// Get the namespace from the volRep or use the failoverpolicy namespace
			namespace := volRep.Namespace
			if namespace == "" {
				namespace = failoverPolicy.Namespace
			}

			// Check if the VolumeReplication exists
			vr := &replicationv1alpha1.VolumeReplication{}
			if err := r.Get(ctx, types.NamespacedName{Name: volRep.Name, Namespace: namespace}, vr); err != nil {
				if errors.IsNotFound(err) {
					log.Info("VolumeReplication not found", "name", volRep.Name, "namespace", namespace)
					notFoundCount++
				} else {
					log.Error(err, "Failed to get VolumeReplication", "name", volRep.Name, "namespace", namespace)
				}
			} else {
				log.V(1).Info("VolumeReplication found", "name", volRep.Name, "namespace", namespace, "state", vr.Status.State)
			}
		}

		if notFoundCount > 0 {
			log.Info("Some VolumeReplication resources are missing", "notFound", notFoundCount, "total", len(volReps))
		}
	}

	// Get the desired state from annotations or spec
	desiredState := r.getDesiredState(ctx, failoverPolicy)

	// Update the status to reflect the desired state (regardless of whether we'll process workloads)
	if err := statusMgr.UpdateFailoverStatus(ctx, failoverPolicy, false, ""); err != nil {
		log.Error(err, "Failed to update FailoverPolicy status")
		return ctrl.Result{}, err
	}

	// Process VolumeReplications if any are defined
	if len(volReps) > 0 {
		vrNames := getResourceNames(volReps)
		if err := volRepMgr.ProcessVolumeReplications(ctx, failoverPolicy.Namespace, vrNames, desiredState); err != nil {
			log.Error(err, "Failed to process VolumeReplications")
			return ctrl.Result{}, err
		}
	}

	// Convert legacy fields to new format for processing
	managedResources := convertLegacyFields(failoverPolicy)

	// Extract resource references by type from the managedResources field
	virtualServices := filterResourcesByKind(managedResources, "VirtualService")
	deployments := filterResourcesByKind(managedResources, "Deployment")
	statefulSets := filterResourcesByKind(managedResources, "StatefulSet")
	cronJobs := filterResourcesByKind(managedResources, "CronJob")
	helmReleases := filterResourcesByKind(managedResources, "HelmRelease")
	kustomizations := filterResourcesByKind(managedResources, "Kustomization")

	// Process VirtualServices if any are defined
	virtualServiceMgr := virtualservice.NewManager(r.Client)
	if len(virtualServices) > 0 {
		vsNames := getResourceNames(virtualServices)
		virtualServiceMgr.ProcessVirtualServices(ctx, failoverPolicy.Namespace, vsNames, desiredState)
	}

	// Handle synchronization of workloads based on the policy mode
	workloadMgr := workload.NewManager(r.Client)
	fluxMgr := flux.NewManager(r.Client)

	if desiredState == "passive" {
		// In passive mode, scale down workloads directly if reconciliation is not blocked
		if !isReconciliationBlocked(failoverPolicy) {
			log.Info("Processing workloads in passive mode")

			// Convert ManagedResource slices to ResourceReference slices for the workload manager
			deploymentRefs := convertToResourceReferences(deployments)
			statefulSetRefs := convertToResourceReferences(statefulSets)
			cronJobRefs := convertToResourceReferences(cronJobs)

			// Process regular K8s workloads
			if err := workload.ProcessWorkloads(ctx, workloadMgr, deploymentRefs, statefulSetRefs, cronJobRefs, desiredState, failoverPolicy.Namespace); err != nil {
				log.Error(err, "Failed to process workloads")
				return ctrl.Result{}, err
			}

			// Convert ManagedResource slices to strings for the flux manager
			helmReleaseRefs := convertToResourceReferences(helmReleases)
			kustomizationRefs := convertToResourceReferences(kustomizations)

			// Process Flux resources
			if err := fluxMgr.ProcessFluxResources(ctx, helmReleaseRefs, kustomizationRefs, failoverPolicy.Namespace, desiredState); err != nil {
				log.Error(err, "Failed to process flux resources")
				return ctrl.Result{}, err
			}
		} else {
			log.Info("Reconciliation blocked in passive mode", "annotation", ReconcileAnnotation)
		}
	} else if desiredState == "active" {
		// In active mode - let Flux handle workload management to restore state
		log.Info("In active mode - Flux will handle restoring workloads")

		// Convert ManagedResource slices to ResourceReference objects for the flux manager
		helmReleaseRefs := convertToResourceReferences(helmReleases)
		kustomizationRefs := convertToResourceReferences(kustomizations)

		// Process Flux resources to enable them
		if err := fluxMgr.ProcessFluxResources(ctx, helmReleaseRefs, kustomizationRefs, failoverPolicy.Namespace, desiredState); err != nil {
			log.Error(err, "Failed to process flux resources")
			return ctrl.Result{}, err
		}

		// Handle CronJobs directly (since they need to be unsuspended)
		if len(cronJobs) > 0 {
			cronJobRefs := convertToResourceReferences(cronJobs)
			if err := workload.ProcessCronJobs(ctx, workloadMgr, cronJobRefs, desiredState, failoverPolicy.Namespace); err != nil {
				log.Error(err, "Failed to process cronjobs")
				return ctrl.Result{}, err
			}
		}
	}

	// Always update the health status every reconcile
	if err := statusMgr.UpdateStatus(ctx, failoverPolicy); err != nil {
		log.Error(err, "Failed to update health status")
		return ctrl.Result{}, err
	}

	// Update the FailoverPolicy in the cluster
	if err := r.Status().Update(ctx, failoverPolicy); err != nil {
		log.Error(err, "Failed to update FailoverPolicy status")
		return ctrl.Result{}, err
	}

	// Determine the next reconciliation time based on the desired state or health check interval, whichever is shorter
	var requeueTime time.Duration
	if desiredState == "passive" {
		requeueTime = DefaultPassiveReconcileInterval
	} else {
		requeueTime = DefaultActiveReconcileInterval
	}

	// Ensure we don't wait longer than the health check interval
	if requeueTime > HealthCheckInterval {
		requeueTime = HealthCheckInterval
	}

	log.Info("Reconciliation completed", "nextReconcileIn", requeueTime)
	return ctrl.Result{RequeueAfter: requeueTime}, nil
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

// groupResourcesByType organizes resources from the new Components structure and parentFluxResources
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

	// Add resources from Components structure
	for _, component := range policy.Spec.Components {
		// Process workloads in each component
		for _, workload := range component.Workloads {
			ref := crdv1alpha1.ResourceReference{
				Name: workload.Name,
				// Use policy namespace by default
				Namespace: "",
			}

			switch workload.Kind {
			case "Deployment":
				result.deployments = append(result.deployments, ref)
			case "StatefulSet":
				result.statefulSets = append(result.statefulSets, ref)
			case "CronJob":
				result.cronJobs = append(result.cronJobs, ref)
			}
		}

		// Process volume replications in each component
		for _, vrName := range component.VolumeReplications {
			ref := crdv1alpha1.ResourceReference{
				Name: vrName,
				// Use policy namespace by default
				Namespace: "",
			}
			result.volumeReplications = append(result.volumeReplications, ref)
		}

		// Process virtual services in each component
		for _, vsName := range component.VirtualServices {
			ref := crdv1alpha1.ResourceReference{
				Name: vsName,
				// Use policy namespace by default
				Namespace: "",
			}
			result.virtualServices = append(result.virtualServices, ref)
		}
	}

	// Process parent Flux resources
	for _, fluxResource := range policy.Spec.ParentFluxResources {
		ref := crdv1alpha1.ResourceReference{
			Name: fluxResource.Name,
			// Use policy namespace by default
			Namespace: "",
		}

		switch fluxResource.Kind {
		case "HelmRelease":
			result.helmReleases = append(result.helmReleases, ref)
		case "Kustomization":
			result.kustomizations = append(result.kustomizations, ref)
		}
	}

	// Add resources from managedResources field (Legacy support)
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

// getResourceNames extracts the names from a list of ManagedResource items
func getResourceNames(resources []crdv1alpha1.ManagedResource) []string {
	names := make([]string, 0, len(resources))
	for _, res := range resources {
		names = append(names, res.Name)
	}
	return names
}

// convertToResourceReferences converts ManagedResource items to ResourceReference items
func convertToResourceReferences(resources []crdv1alpha1.ManagedResource) []crdv1alpha1.ResourceReference {
	refs := make([]crdv1alpha1.ResourceReference, 0, len(resources))
	for _, res := range resources {
		refs = append(refs, crdv1alpha1.ResourceReference{
			Name:      res.Name,
			Namespace: res.Namespace,
		})
	}
	return refs
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

			// Check the components structure first
			for _, component := range policy.Spec.Components {
				for _, workload := range component.Workloads {
					if workload.Kind == objKind && workload.Name == objName {
						shouldEnqueue = true
						break
					}
				}
				if shouldEnqueue {
					break
				}
			}

			// Check the new parentFluxResources for Flux resources
			if !shouldEnqueue && (objKind == "HelmRelease" || objKind == "Kustomization") {
				for _, fluxResource := range policy.Spec.ParentFluxResources {
					if fluxResource.Kind == objKind && fluxResource.Name == objName {
						shouldEnqueue = true
						break
					}
				}
			}

			// Check the managedResources field if not found yet
			if !shouldEnqueue {
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
				case "HelmRelease":
					for _, ref := range policy.Spec.HelmReleases {
						if ref.Name == objName {
							shouldEnqueue = true
							break
						}
					}
				case "Kustomization":
					for _, ref := range policy.Spec.Kustomizations {
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

			// Check in components first
			for _, component := range policy.Spec.Components {
				for _, vrName := range component.VolumeReplications {
					if vrName == objName {
						shouldEnqueue = true
						break
					}
				}
				if shouldEnqueue {
					break
				}
			}

			// Check managedResources field if not found yet
			if !shouldEnqueue {
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

			// Check in components first
			for _, component := range policy.Spec.Components {
				for _, vsName := range component.VirtualServices {
					if vsName == objName {
						shouldEnqueue = true
						break
					}
				}
				if shouldEnqueue {
					break
				}
			}

			// Check managedResources field if not found yet
			if !shouldEnqueue {
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

// Helper functions for managing resources
func getVolumeReplications(policy *crdv1alpha1.FailoverPolicy) []crdv1alpha1.ManagedResource {
	var volReps []crdv1alpha1.ManagedResource

	// First check the new managedResources field
	if policy.Spec.ManagedResources != nil {
		for _, res := range policy.Spec.ManagedResources {
			if res.Kind == "VolumeReplication" {
				volReps = append(volReps, res)
			}
		}
	}

	// For backward compatibility, check VolumeReplications field if no VolumeReplications were found
	if len(volReps) == 0 && len(policy.Spec.VolumeReplications) > 0 {
		for _, ref := range policy.Spec.VolumeReplications {
			volReps = append(volReps, crdv1alpha1.ManagedResource{
				Kind:      "VolumeReplication",
				Name:      ref.Name,
				Namespace: ref.Namespace,
			})
		}
	}

	return volReps
}

// convertLegacyFields handles backward compatibility by converting legacy fields to the new format
func convertLegacyFields(policy *crdv1alpha1.FailoverPolicy) []crdv1alpha1.ManagedResource {
	var resources []crdv1alpha1.ManagedResource

	// If managedResources is already populated, use it
	if policy.Spec.ManagedResources != nil && len(policy.Spec.ManagedResources) > 0 {
		return policy.Spec.ManagedResources
	}

	// Otherwise convert legacy fields to ManagedResource format

	// Convert VolumeReplications
	for _, ref := range policy.Spec.VolumeReplications {
		resources = append(resources, crdv1alpha1.ManagedResource{
			Kind:      "VolumeReplication",
			Name:      ref.Name,
			Namespace: ref.Namespace,
		})
	}

	// Convert Deployments
	for _, ref := range policy.Spec.Deployments {
		resources = append(resources, crdv1alpha1.ManagedResource{
			Kind:      "Deployment",
			Name:      ref.Name,
			Namespace: ref.Namespace,
		})
	}

	// Convert StatefulSets
	for _, ref := range policy.Spec.StatefulSets {
		resources = append(resources, crdv1alpha1.ManagedResource{
			Kind:      "StatefulSet",
			Name:      ref.Name,
			Namespace: ref.Namespace,
		})
	}

	// Convert CronJobs
	for _, ref := range policy.Spec.CronJobs {
		resources = append(resources, crdv1alpha1.ManagedResource{
			Kind:      "CronJob",
			Name:      ref.Name,
			Namespace: ref.Namespace,
		})
	}

	// Convert Flux resources
	for _, ref := range policy.Spec.HelmReleases {
		resources = append(resources, crdv1alpha1.ManagedResource{
			Kind:      "HelmRelease",
			Name:      ref.Name,
			Namespace: ref.Namespace,
		})
	}

	for _, ref := range policy.Spec.Kustomizations {
		resources = append(resources, crdv1alpha1.ManagedResource{
			Kind:      "Kustomization",
			Name:      ref.Name,
			Namespace: ref.Namespace,
		})
	}

	// Convert VirtualServices
	for _, ref := range policy.Spec.VirtualServices {
		resources = append(resources, crdv1alpha1.ManagedResource{
			Kind:      "VirtualService",
			Name:      ref.Name,
			Namespace: ref.Namespace,
		})
	}

	return resources
}

// filterResourcesByKind returns a list of resources of the specified kind
func filterResourcesByKind(resources []crdv1alpha1.ManagedResource, kind string) []crdv1alpha1.ManagedResource {
	var filtered []crdv1alpha1.ManagedResource
	for _, res := range resources {
		if res.Kind == kind {
			filtered = append(filtered, res)
		}
	}
	return filtered
}

// getDesiredState determines the desired state from annotations or spec
func (r *FailoverPolicyReconciler) getDesiredState(ctx context.Context, policy *crdv1alpha1.FailoverPolicy) string {
	log := log.FromContext(ctx)

	// Default is active if not specified
	desiredState := "active"

	// First check annotation (preferred method)
	if anno, ok := policy.Annotations["failover-operator.hahomelabs.com/desired-state"]; ok {
		// Normalize annotation value
		desiredAnno := strings.ToLower(anno)

		// Handle both new and old terminology
		if desiredAnno == "active" || desiredAnno == "primary" {
			desiredState = "active"
		} else if desiredAnno == "passive" || desiredAnno == "secondary" || desiredAnno == "inactive" {
			desiredState = "passive"
		} else {
			log.Info("Unknown desired state in annotation, using default",
				"annotation", desiredAnno,
				"default", desiredState)
		}
	} else if policy.Spec.DesiredState != "" {
		// Legacy field - map old terminology to new
		if strings.ToLower(policy.Spec.DesiredState) == "primary" {
			desiredState = "active"
		} else if strings.ToLower(policy.Spec.DesiredState) == "secondary" {
			desiredState = "passive"
		} else {
			desiredState = strings.ToLower(policy.Spec.DesiredState)
		}
	}

	return desiredState
}

// isReconciliationBlocked checks if reconciliation is blocked by an annotation
func isReconciliationBlocked(policy *crdv1alpha1.FailoverPolicy) bool {
	if val, exists := policy.Annotations[ReconcileAnnotation]; exists {
		return strings.ToLower(val) == "true"
	}
	return false
}
