package managedpostgres

import (
	"context"
	"slices"
)

// Optional private capability. The provider must inventory every native
// database except its authenticated maintenance database, including templates,
// closed databases and foreign owners. A partial provider API list is insufficient.
// This read provides original selection intent, not checkpoint/readiness evidence.
type CheckpointConnectionDiscoveryProvider interface {
	DiscoverCheckpointConnections(context.Context, RestoreSourceDefinition, CheckpointMaintenance, CheckpointConnectionIdentity) (CheckpointConnectionRequest, error)
}

func (s *Service) DiscoverCheckpointConnections(ctx context.Context, d RestoreSourceDefinition, m CheckpointMaintenance, identity CheckpointConnectionIdentity) (CheckpointConnectionRequest, error) {
	if err := ctx.Err(); err != nil {
		return CheckpointConnectionRequest{}, err
	}
	if !ValidCheckpointConnectionRecovery(m, identity) || identity.SourceResourceID != d.DataResourceID {
		return CheckpointConnectionRequest{}, ErrInvalid
	}
	backend, err := s.checkpointBackend(d)
	if err != nil {
		return CheckpointConnectionRequest{}, err
	}
	provider, ok := backend.Provider.(CheckpointConnectionDiscoveryProvider)
	if !ok {
		return CheckpointConnectionRequest{}, ErrUnsupported
	}
	providerCtx, cancel := context.WithTimeout(ctx, s.providerTimeout)
	defer cancel()
	actual, err := provider.DiscoverCheckpointConnections(providerCtx, d, m, identity)
	if err == nil {
		err = providerCtx.Err()
	}
	if err != nil {
		return CheckpointConnectionRequest{}, err
	}
	if actual.CheckpointConnectionIdentity != identity || actual.Validate() != nil || !slices.IsSorted(actual.DatabaseNames) {
		return CheckpointConnectionRequest{}, ErrConflict
	}
	actual.DatabaseNames = slices.Clone(actual.DatabaseNames)
	if err := providerCtx.Err(); err != nil {
		return CheckpointConnectionRequest{}, err
	}
	return actual, nil
}
