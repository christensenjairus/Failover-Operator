package volumereplications

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// Manager handles operations related to VolumeReplication resources
type Manager struct {
	client client.Client
}

// NewManager creates a new VolumeReplication manager
func NewManager(client client.Client) *Manager {
	return &Manager{
		client: client,
	}
}

// SetVolumeReplicationState updates a VolumeReplication resource's replication state
func (m *Manager) SetVolumeReplicationState(ctx context.Context, name, namespace, state string) error {
	logger := log.FromContext(ctx).WithValues("volumereplication", name, "namespace", namespace)
	logger.Info("Setting VolumeReplication state", "desiredState", state)

	// TODO: Implement VolumeReplication state update logic
	// 1. Fetch VolumeReplication as unstructured
	// 2. Update replicationState based on input state
	// 3. Update VolumeReplication

	return nil
}

// SetPrimary sets a VolumeReplication to primary state
func (m *Manager) SetPrimary(ctx context.Context, name, namespace string) error {
	return m.SetVolumeReplicationState(ctx, name, namespace, "primary")
}

// SetSecondary sets a VolumeReplication to secondary state
func (m *Manager) SetSecondary(ctx context.Context, name, namespace string) error {
	return m.SetVolumeReplicationState(ctx, name, namespace, "secondary")
}

// ProcessVolumeReplications handles updating all VolumeReplications for a component
func (m *Manager) ProcessVolumeReplications(ctx context.Context, names []string, namespace string, makePrimary bool) error {
	logger := log.FromContext(ctx)
	desiredState := "secondary"
	if makePrimary {
		desiredState = "primary"
	}

	logger.Info("Processing VolumeReplications", "count", len(names), "desiredState", desiredState)

	for _, name := range names {
		if err := m.SetVolumeReplicationState(ctx, name, namespace, desiredState); err != nil {
			logger.Error(err, "Failed to update VolumeReplication", "name", name)
			return err
		}
	}

	return nil
}
