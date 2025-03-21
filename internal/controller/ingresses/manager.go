package ingresses

import (
	"context"
	"fmt"
	"time"

	networkingv1 "k8s.io/api/networking/v1"
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

	// DNSControllerAnnotation is the annotation used to control DNS behavior
	DNSControllerAnnotation = "dns-controller.kubernetes.io/enabled"

	// DNSControllerEnabled is the value to enable DNS controller for an Ingress
	DNSControllerEnabled = "true"

	// DNSControllerDisabled is the value to disable DNS controller for an Ingress
	DNSControllerDisabled = "false"
)

// Manager handles operations related to Ingress resources
// This manager provides methods to control DNS routing and annotations during failover
type Manager struct {
	// Kubernetes client for API interactions
	client client.Client
}

// NewManager creates a new Ingress manager
// The client is used to interact with the Kubernetes API server
func NewManager(client client.Client) *Manager {
	return &Manager{
		client: client,
	}
}

// UpdateIngress updates an Ingress resource based on the active state
// If active is true, enables DNS controller annotation (primary cluster)
// If active is false, disables DNS controller annotation (standby cluster)
func (m *Manager) UpdateIngress(ctx context.Context, name, namespace, state string) (bool, error) {
	logger := log.FromContext(ctx).WithValues("ingress", name, "namespace", namespace)
	logger.Info("Updating Ingress", "state", state)

	// Get the Ingress
	ingress := &networkingv1.Ingress{}
	err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, ingress)
	if err != nil {
		logger.Error(err, "Failed to get Ingress")
		return false, err
	}

	// Determine if this is active or passive
	isActive := state == "active"

	// Add Flux annotation to prevent Flux from reconciling during failover
	if ingress.Annotations == nil {
		ingress.Annotations = make(map[string]string)
	}
	ingress.Annotations[FluxReconcileAnnotation] = DisabledValue

	// Set DNS controller annotation based on active state
	updated := false
	if isActive {
		if ingress.Annotations[DNSControllerAnnotation] != DNSControllerEnabled {
			ingress.Annotations[DNSControllerAnnotation] = DNSControllerEnabled
			updated = true
		}
	} else {
		if ingress.Annotations[DNSControllerAnnotation] != DNSControllerDisabled {
			ingress.Annotations[DNSControllerAnnotation] = DNSControllerDisabled
			updated = true
		}
	}

	// Update the Ingress if needed
	if updated {
		err = m.client.Update(ctx, ingress)
		if err != nil {
			logger.Error(err, "Failed to update Ingress")
			return false, err
		}
		logger.Info("Successfully updated Ingress", "state", state)
	} else {
		logger.Info("No changes needed for Ingress", "state", state)
	}

	return updated, nil
}

// ProcessIngresses processes a list of Ingresses
// If active is true, enables DNS controller annotation on all Ingresses
// If active is false, disables DNS controller annotation on all Ingresses
func (m *Manager) ProcessIngresses(ctx context.Context, namespace string, names []string, active bool) {
	logger := log.FromContext(ctx).WithValues("namespace", namespace)
	state := "passive"
	if active {
		state = "active"
	}
	logger.Info("Processing Ingresses", "count", len(names), "state", state)

	for _, name := range names {
		_, err := m.UpdateIngress(ctx, name, namespace, state)
		if err != nil {
			logger.Error(err, "Failed to update Ingress", "ingress", name)
		}
	}
}

// AddFluxAnnotation adds the Flux reconcile annotation with a disabled value
// This prevents Flux from reconciling the Ingress during failover operations
func (m *Manager) AddFluxAnnotation(ctx context.Context, name, namespace string) error {
	logger := log.FromContext(ctx).WithValues("ingress", name, "namespace", namespace)
	logger.Info("Adding Flux disable annotation")
	return m.AddAnnotation(ctx, name, namespace, FluxReconcileAnnotation, DisabledValue)
}

// RemoveFluxAnnotation removes the Flux reconcile annotation
// This allows Flux to resume reconciling the Ingress
func (m *Manager) RemoveFluxAnnotation(ctx context.Context, name, namespace string) error {
	logger := log.FromContext(ctx).WithValues("ingress", name, "namespace", namespace)
	logger.Info("Removing Flux disable annotation")
	return m.RemoveAnnotation(ctx, name, namespace, FluxReconcileAnnotation)
}

// AddAnnotation adds an annotation to an Ingress
// Useful for adding metadata or configuration to the Ingress
func (m *Manager) AddAnnotation(ctx context.Context, name, namespace, key, value string) error {
	logger := log.FromContext(ctx).WithValues("ingress", name, "namespace", namespace)
	logger.Info("Adding annotation", "key", key, "value", value)

	// Get the Ingress
	ingress := &networkingv1.Ingress{}
	err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, ingress)
	if err != nil {
		logger.Error(err, "Failed to get Ingress")
		return err
	}

	// Add the annotation
	if ingress.Annotations == nil {
		ingress.Annotations = make(map[string]string)
	}
	ingress.Annotations[key] = value

	// Update the Ingress
	err = m.client.Update(ctx, ingress)
	if err != nil {
		logger.Error(err, "Failed to update Ingress with annotation")
		return err
	}

	return nil
}

// RemoveAnnotation removes an annotation from an Ingress
// Useful for cleaning up or changing the behavior of the Ingress
func (m *Manager) RemoveAnnotation(ctx context.Context, name, namespace, key string) error {
	logger := log.FromContext(ctx).WithValues("ingress", name, "namespace", namespace)
	logger.Info("Removing annotation", "key", key)

	// Get the Ingress
	ingress := &networkingv1.Ingress{}
	err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, ingress)
	if err != nil {
		logger.Error(err, "Failed to get Ingress")
		return err
	}

	// Remove the annotation if it exists
	if ingress.Annotations != nil {
		if _, exists := ingress.Annotations[key]; exists {
			delete(ingress.Annotations, key)

			// Update the Ingress
			err = m.client.Update(ctx, ingress)
			if err != nil {
				logger.Error(err, "Failed to update Ingress after removing annotation")
				return err
			}
		}
	}

	return nil
}

// GetAnnotation gets the value of an annotation from an Ingress
// Returns the value and a boolean indicating if the annotation exists
func (m *Manager) GetAnnotation(ctx context.Context, name, namespace, key string) (string, bool, error) {
	logger := log.FromContext(ctx).WithValues("ingress", name, "namespace", namespace)
	logger.V(1).Info("Getting annotation", "key", key)

	// Get the Ingress
	ingress := &networkingv1.Ingress{}
	err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, ingress)
	if err != nil {
		logger.Error(err, "Failed to get Ingress")
		return "", false, err
	}

	// Get the annotation if it exists
	if ingress.Annotations != nil {
		if value, exists := ingress.Annotations[key]; exists {
			return value, true, nil
		}
	}

	return "", false, nil
}

// SetDNSController sets the DNS controller annotation for an Ingress
// If enable is true, enables DNS controller to route traffic to this Ingress
// If enable is false, disables DNS controller to stop routing traffic to this Ingress
func (m *Manager) SetDNSController(ctx context.Context, name, namespace string, enable bool) error {
	logger := log.FromContext(ctx).WithValues("ingress", name, "namespace", namespace)
	logger.Info("Setting DNS controller", "enable", enable)

	value := DNSControllerDisabled
	if enable {
		value = DNSControllerEnabled
	}

	return m.AddAnnotation(ctx, name, namespace, DNSControllerAnnotation, value)
}

// IsPrimary checks if an Ingress is configured as primary
// An Ingress is primary when its DNS controller annotation is enabled
func (m *Manager) IsPrimary(ctx context.Context, name, namespace string) (bool, error) {
	logger := log.FromContext(ctx).WithValues("ingress", name, "namespace", namespace)
	logger.V(1).Info("Checking if Ingress is primary")

	value, exists, err := m.GetAnnotation(ctx, name, namespace, DNSControllerAnnotation)
	if err != nil {
		return false, err
	}

	if !exists {
		logger.V(1).Info("DNS controller annotation not found, assuming secondary")
		return false, nil
	}

	return value == DNSControllerEnabled, nil
}

// IsSecondary checks if an Ingress is configured as secondary
// An Ingress is secondary when its DNS controller annotation is disabled or not set
func (m *Manager) IsSecondary(ctx context.Context, name, namespace string) (bool, error) {
	isPrimary, err := m.IsPrimary(ctx, name, namespace)
	if err != nil {
		return false, err
	}
	return !isPrimary, nil
}

// IsReady checks if an Ingress is ready (has an IP or hostname)
// Used to determine if the Ingress has been provisioned by the controller
func (m *Manager) IsReady(ctx context.Context, name, namespace string) (bool, error) {
	logger := log.FromContext(ctx).WithValues("ingress", name, "namespace", namespace)
	logger.V(1).Info("Checking if Ingress is ready")

	// Get the Ingress
	ingress := &networkingv1.Ingress{}
	err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, ingress)
	if err != nil {
		logger.Error(err, "Failed to get Ingress")
		return false, err
	}

	// Check if Ingress has status (meaning it has been configured by the controller)
	if len(ingress.Status.LoadBalancer.Ingress) == 0 {
		logger.V(1).Info("Ingress not ready, no LoadBalancer status")
		return false, nil
	}

	// Check if at least one entry has an IP or Hostname
	for _, ing := range ingress.Status.LoadBalancer.Ingress {
		if ing.IP != "" || ing.Hostname != "" {
			logger.V(1).Info("Ingress is ready", "ip", ing.IP, "hostname", ing.Hostname)
			return true, nil
		}
	}

	logger.V(1).Info("Ingress not ready, LoadBalancer status incomplete")
	return false, nil
}

// WaitForReady waits for an Ingress to be ready (have an IP or hostname)
// Useful during failover to ensure Ingress is properly provisioned before proceeding
func (m *Manager) WaitForReady(ctx context.Context, name, namespace string, timeoutSeconds int) error {
	logger := log.FromContext(ctx).WithValues("ingress", name, "namespace", namespace)
	logger.Info("Waiting for Ingress to be ready", "timeout", timeoutSeconds)

	return wait.PollImmediate(2*time.Second, time.Duration(timeoutSeconds)*time.Second, func() (bool, error) {
		ready, err := m.IsReady(ctx, name, namespace)
		if err != nil {
			logger.Error(err, "Failed to check if Ingress is ready")
			return false, nil // Continue polling
		}

		if ready {
			logger.Info("Ingress is ready")
			return true, nil
		}

		logger.V(1).Info("Ingress not yet ready")
		return false, nil
	})
}

// WaitForAllIngressesReady waits for all Ingresses to be ready
// Used during failover to ensure all Ingresses are properly provisioned
func (m *Manager) WaitForAllIngressesReady(ctx context.Context, namespace string, names []string, timeoutSeconds int) error {
	logger := log.FromContext(ctx).WithValues("namespace", namespace)
	logger.Info("Waiting for all Ingresses to be ready", "count", len(names), "timeout", timeoutSeconds)

	for _, name := range names {
		if err := m.WaitForReady(ctx, name, namespace, timeoutSeconds); err != nil {
			logger.Error(err, "Timeout waiting for Ingress to be ready", "ingress", name)
			return fmt.Errorf("timeout waiting for Ingress %s to be ready: %w", name, err)
		}
	}

	logger.Info("All Ingresses are ready")
	return nil
}
