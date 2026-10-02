package managedpostgres

import (
	"context"
	"time"
)

// The barrier owner is operation-specific; the maintenance owner has its own
// stable private identity and may be reused by successive clone operations.
type CheckpointConnectionIdentity struct {
	OwnerToken, SourceResourceID string
}

type CheckpointConnectionTerminal struct {
	CheckpointConnectionIdentity
	State      string
	ReleasedAt time.Time
}

// Abandon persists an operation tombstone even before a delayed close arrives.
// Observe is a separate authenticated read of the exact remote terminal record.
// This capability alone provides no common checkpoint or writer coverage proof.
type CheckpointConnectionRecoveryProvider interface {
	AbandonCheckpointConnections(context.Context, RestoreSourceDefinition, CheckpointMaintenance, CheckpointConnectionIdentity) (CheckpointConnectionTerminal, error)
	ObserveCheckpointConnections(context.Context, RestoreSourceDefinition, CheckpointMaintenance, CheckpointConnectionIdentity) (CheckpointConnectionTerminal, error)
}

func (s *Service) AbandonCheckpointConnections(ctx context.Context, definition RestoreSourceDefinition, maintenance CheckpointMaintenance, identity CheckpointConnectionIdentity) (CheckpointConnectionTerminal, error) {
	return s.recoverCheckpointConnections(ctx, definition, maintenance, identity, true)
}

func (s *Service) ObserveCheckpointConnections(ctx context.Context, definition RestoreSourceDefinition, maintenance CheckpointMaintenance, identity CheckpointConnectionIdentity) (CheckpointConnectionTerminal, error) {
	return s.recoverCheckpointConnections(ctx, definition, maintenance, identity, false)
}

func (s *Service) recoverCheckpointConnections(ctx context.Context, definition RestoreSourceDefinition, maintenance CheckpointMaintenance, identity CheckpointConnectionIdentity, abandon bool) (CheckpointConnectionTerminal, error) {
	if !ValidCheckpointConnectionRecovery(maintenance, identity) || identity.SourceResourceID != definition.DataResourceID {
		return CheckpointConnectionTerminal{}, ErrInvalid
	}
	backend, err := s.checkpointBackend(definition)
	if err != nil {
		return CheckpointConnectionTerminal{}, err
	}
	provider, ok := backend.Provider.(CheckpointConnectionRecoveryProvider)
	if !ok {
		return CheckpointConnectionTerminal{}, ErrUnsupported
	}
	providerCtx, cancel := context.WithTimeout(ctx, s.providerTimeout)
	defer cancel()
	var actual CheckpointConnectionTerminal
	if abandon {
		actual, err = provider.AbandonCheckpointConnections(providerCtx, definition, maintenance, identity)
	} else {
		actual, err = provider.ObserveCheckpointConnections(providerCtx, definition, maintenance, identity)
	}
	if err != nil {
		return CheckpointConnectionTerminal{}, err
	}
	if actual.CheckpointConnectionIdentity != identity || actual.ReleasedAt.IsZero() || actual.State != "released" && actual.State != "abandoned" {
		return CheckpointConnectionTerminal{}, ErrConflict
	}
	return actual, nil
}

func ValidCheckpointConnectionRecovery(maintenance CheckpointMaintenance, identity CheckpointConnectionIdentity) bool {
	m := CheckpointMaintenanceRequest{OwnerToken: maintenance.OwnerToken, SourceResourceID: maintenance.SourceResourceID,
		Phase: "ready", OwnerOID: maintenance.OwnerOID, DatabaseOID: maintenance.DatabaseOID}
	i := CheckpointMaintenanceRequest{OwnerToken: identity.OwnerToken, SourceResourceID: identity.SourceResourceID, Phase: "role"}
	return maintenance.State == "ready" && m.Validate() == nil && i.Validate() == nil &&
		maintenance.SourceResourceID == identity.SourceResourceID && maintenance.OwnerToken != identity.OwnerToken
}
