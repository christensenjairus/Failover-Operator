package virtualservice

import (
	"context"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// Manager handles operations related to VirtualService resources
type Manager struct {
	client client.Client
}

// NewManager creates a new VirtualService manager
func NewManager(client client.Client) *Manager {
	return &Manager{
		client: client,
	}
}

// UpdateVirtualService updates a VirtualService resource based on the desired state
func (m *Manager) UpdateVirtualService(ctx context.Context, name, namespace, state string) (bool, error) {
	virtualService := &unstructured.Unstructured{}
	virtualService.SetAPIVersion("networking.istio.io/v1")
	virtualService.SetKind("VirtualService")

	// Fetch VirtualService object
	err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, virtualService)
	if err != nil {
		return false, client.IgnoreNotFound(err)
	}

	// Ensure annotations exist
	annotations := virtualService.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}

	// Determine the correct annotation based on failover state
	desiredAnnotation := "ignore"
	if strings.ToLower(state) == "primary" {
		desiredAnnotation = "dns-controller"
	}

	// Check if update is needed
	currentAnnotation, found := annotations["external-dns.alpha.kubernetes.io/controller"]
	if !found || currentAnnotation != desiredAnnotation {
		annotations["external-dns.alpha.kubernetes.io/controller"] = desiredAnnotation
		virtualService.SetAnnotations(annotations)

		if err := m.client.Update(ctx, virtualService); err != nil {
			return false, err
		}
		return true, nil // Indicates an update was made
	}

	return false, nil // No update needed
}

// ProcessVirtualServices handles the processing of all VirtualServices for a FailoverPolicy
func (m *Manager) ProcessVirtualServices(ctx context.Context, namespace string, vsNames []string, desiredState string) {
	log := log.FromContext(ctx)

	for _, vsName := range vsNames {
		log.Info("Checking VirtualService update", "VirtualService", vsName)

		updated, err := m.UpdateVirtualService(ctx, vsName, namespace, desiredState)
		if err != nil {
			log.Error(err, "Failed to update VirtualService", "VirtualService", vsName)
			continue
		}
		if updated {
			log.Info("Updated VirtualService", "VirtualService", vsName)
		}
	}
}
