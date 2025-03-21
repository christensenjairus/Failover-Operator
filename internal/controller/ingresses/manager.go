package ingresses

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// Manager handles operations related to Ingress resources
type Manager struct {
	client client.Client
}

// NewManager creates a new Ingress manager
func NewManager(client client.Client) *Manager {
	return &Manager{
		client: client,
	}
}

// UpdateIngress updates an Ingress resource based on the desired state
func (m *Manager) UpdateIngress(ctx context.Context, name, namespace, state string) (bool, error) {
	logger := log.FromContext(ctx).WithValues("ingress", name, "namespace", namespace)
	logger.Info("Updating Ingress", "desiredState", state)

	// TODO: Implement Ingress update logic
	// 1. Fetch Ingress
	// 2. Update based on desired state (e.g., modify annotations, rules, etc.)
	// 3. Update Ingress

	return false, nil
}

// ProcessIngresses handles updating all Ingresses for a component
func (m *Manager) ProcessIngresses(ctx context.Context, namespace string, ingressNames []string, active bool) {
	logger := log.FromContext(ctx)
	desiredState := "passive"
	if active {
		desiredState = "active"
	}

	logger.Info("Processing Ingresses", "count", len(ingressNames), "desiredState", desiredState)

	for _, ingressName := range ingressNames {
		logger.Info("Updating Ingress", "name", ingressName)

		_, err := m.UpdateIngress(ctx, ingressName, namespace, desiredState)
		if err != nil {
			logger.Error(err, "Failed to update Ingress", "name", ingressName)
		}
	}
}

// AddFluxAnnotation adds the flux reconcile annotation to disable automatic reconciliation
func (m *Manager) AddFluxAnnotation(ctx context.Context, name, namespace string) error {
	logger := log.FromContext(ctx).WithValues("ingress", name, "namespace", namespace)
	logger.Info("Adding Flux annotation to Ingress")

	// TODO: Implement adding flux annotation to Ingress
	// 1. Get the Ingress
	// 2. Add the annotation
	// 3. Update the Ingress

	return nil
}

// RemoveFluxAnnotation removes the flux reconcile annotation to enable automatic reconciliation
func (m *Manager) RemoveFluxAnnotation(ctx context.Context, name, namespace string) error {
	logger := log.FromContext(ctx).WithValues("ingress", name, "namespace", namespace)
	logger.Info("Removing Flux annotation from Ingress")

	// TODO: Implement removing flux annotation from Ingress
	// 1. Get the Ingress
	// 2. Remove the annotation
	// 3. Update the Ingress

	return nil
}
