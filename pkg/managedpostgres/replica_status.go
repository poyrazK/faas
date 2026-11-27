package managedpostgres

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
)

type replicaStatusReader interface {
	ReplicaStatus(context.Context, string) (*api.ManagedPostgresReplicaStatus, error)
}

// ReplicaStatus reads standby evidence for an already-authorized database.
func (s *Service) ReplicaStatus(ctx context.Context, d Database) (*api.ManagedPostgresReplicaStatus, error) {
	if d.Spec.Class != ClassProduction || d.ProviderResourceID == "" {
		return nil, nil
	}
	backend, err := s.registry.Resolve(d.BackendID, d.BackendFingerprint)
	if err != nil {
		return nil, err
	}
	reader, ok := backend.Provider.(replicaStatusReader)
	if !ok {
		return nil, nil
	}
	return reader.ReplicaStatus(ctx, d.ProviderResourceID)
}
