package managedpostgres

import (
	"context"

	"github.com/google/uuid"
)

// CheckpointMaintenanceRequest consumes a private, durably reserved dataset
// owner. The worker must persist each phase's dispatch intent under its live
// source hold before calling the provider. Lease expiry never releases that hold.
type CheckpointMaintenanceRequest struct {
	OwnerToken, SourceResourceID string
	Phase                        string
	OwnerOID, DatabaseOID        uint32
}

type CheckpointMaintenance struct {
	OwnerToken, SourceResourceID, State string
	OwnerOID, DatabaseOID               uint32
}

func (r CheckpointMaintenanceRequest) Validate() error {
	token, err := uuid.Parse(r.OwnerToken)
	if err != nil || token == uuid.Nil || token.String() != r.OwnerToken || !validDataResourceID(r.SourceResourceID) {
		return ErrInvalid
	}
	switch r.Phase {
	case "role":
		if r.OwnerOID != 0 || r.DatabaseOID != 0 {
			return ErrInvalid
		}
	case "database":
		if r.OwnerOID == 0 || r.DatabaseOID != 0 {
			return ErrInvalid
		}
	case "activation", "ready":
		if r.OwnerOID == 0 || r.DatabaseOID == 0 {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

// This optional capability does not advertise complete checkpoint support.
// Every phase recovers the same owner and is idempotent across unknown replies.
// Ready observes through the private database, even while source admission is
// closed. Bootstrap phases must use the authenticated source database.
type CheckpointMaintenanceProvider interface {
	ReconcileCheckpointMaintenance(context.Context, RestoreSourceDefinition, CheckpointMaintenanceRequest) (CheckpointMaintenance, error)
}

// ReconcileCheckpointMaintenance resolves the frozen backend and spec. It is
// internal recovery work for an owned source hold, so it continues when new
// database provisioning is disabled. Its caller authenticates the clone lease,
// dispatch ledger and source catalogue before every call. It neither chooses a
// source point nor grants permission to close or release writers.
func (s *Service) ReconcileCheckpointMaintenance(ctx context.Context, definition RestoreSourceDefinition, request CheckpointMaintenanceRequest) (CheckpointMaintenance, error) {
	if s == nil || s.registry == nil {
		return CheckpointMaintenance{}, ErrUnavailable
	}
	if request.Validate() != nil || definition.Spec.Validate() != nil || definition.BackendID == "" ||
		definition.BackendFingerprint == "" || definition.ProviderResourceID == "" || request.SourceResourceID != definition.DataResourceID {
		return CheckpointMaintenance{}, ErrInvalid
	}
	backend, err := s.checkpointBackend(definition)
	if err != nil {
		return CheckpointMaintenance{}, err
	}
	provider, ok := backend.Provider.(CheckpointMaintenanceProvider)
	if !ok {
		return CheckpointMaintenance{}, ErrUnsupported
	}
	providerCtx, cancel := context.WithTimeout(ctx, s.providerTimeout)
	defer cancel()
	actual, err := provider.ReconcileCheckpointMaintenance(providerCtx, definition, request)
	if err != nil {
		return CheckpointMaintenance{}, err
	}
	if !checkpointMaintenanceMatches(request, actual) {
		return CheckpointMaintenance{}, ErrConflict
	}
	return actual, nil
}

func (s *Service) checkpointBackend(definition RestoreSourceDefinition) (Backend, error) {
	if s == nil || s.registry == nil {
		return Backend{}, ErrUnavailable
	}
	if definition.Spec.Validate() != nil || definition.BackendID == "" || definition.BackendFingerprint == "" ||
		definition.ProviderResourceID == "" || !validDataResourceID(definition.DataResourceID) {
		return Backend{}, ErrInvalid
	}
	backend, err := s.registry.Resolve(definition.BackendID, definition.BackendFingerprint)
	if err != nil {
		return Backend{}, err
	}
	if backend.Region != definition.Spec.Region {
		return Backend{}, ErrConflict
	}
	if err := backend.Capabilities.Supports(definition.Spec); err != nil {
		return Backend{}, err
	}
	return backend, nil
}

func checkpointMaintenanceMatches(r CheckpointMaintenanceRequest, o CheckpointMaintenance) bool {
	if o.OwnerToken != r.OwnerToken || o.SourceResourceID != r.SourceResourceID || o.OwnerOID == 0 ||
		r.OwnerOID != 0 && r.OwnerOID != o.OwnerOID || r.DatabaseOID != 0 && r.DatabaseOID != o.DatabaseOID {
		return false
	}
	switch r.Phase {
	case "role":
		return o.State == "reserved" && o.DatabaseOID == 0
	case "database":
		return o.State == "reserved" && o.DatabaseOID != 0
	case "activation", "ready":
		return o.State == "ready" && o.DatabaseOID != 0
	default:
		return false
	}
}
