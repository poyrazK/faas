package managedpostgres

import (
	"context"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres/checkpoint"
)

// The caller must durably own the exact source and selected database set before
// dispatch. Names alone are not evidence of complete cluster/writer coverage.
// Retries use the same operation owner and set; unknown replies retain the hold.
type CheckpointConnectionRequest = checkpoint.CheckpointConnectionRequest

type CheckpointConnectionDatabase struct {
	OID, OwnerOID            uint32
	Name                     string
	OriginalAllowConnections bool
	Sessions                 int64
}

// This is an authenticated observation of admission and current sessions only.
// ClosedAt is the admission ledger timestamp, not a common data capture point.
type CheckpointConnectionClosure struct {
	CheckpointConnectionIdentity
	State     string
	ClosedAt  time.Time
	Databases []CheckpointConnectionDatabase
	Drained   bool
}

func (CheckpointConnectionClosure) String() string {
	return "[private PostgreSQL checkpoint observation]"
}
func (o CheckpointConnectionClosure) GoString() string { return o.String() }
func (CheckpointConnectionClosure) MarshalJSON() ([]byte, error) {
	return []byte(`"[private PostgreSQL checkpoint observation]"`), nil
}

func (o CheckpointConnectionClosure) Validate(r CheckpointConnectionRequest) error {
	if r.Validate() != nil || o.CheckpointConnectionIdentity != r.CheckpointConnectionIdentity || o.State != "closed" ||
		o.ClosedAt.IsZero() || o.ClosedAt.Year() < 1 || o.ClosedAt.Year() > 9999 || o.ClosedAt.Nanosecond()%1000 != 0 ||
		o.ClosedAt.After(time.Now()) || len(o.Databases) != len(r.DatabaseNames) {
		return ErrConflict
	}
	names := slices.Clone(r.DatabaseNames)
	slices.Sort(names)
	oids := make(map[uint32]bool, len(names))
	drained := true
	for i, db := range o.Databases {
		if db.Name != names[i] || db.OID == 0 || db.OwnerOID == 0 || oids[db.OID] || db.Sessions < 0 {
			return ErrConflict
		}
		oids[db.OID] = true
		drained = drained && db.Sessions == 0
	}
	if o.Drained != drained {
		return ErrConflict
	}
	return nil
}

// Optional private capability. Observe is read-only and must not install a
// ledger or redispatch close. No successful capture release is provided here.
type CheckpointConnectionClosureProvider interface {
	CloseCheckpointConnections(context.Context, RestoreSourceDefinition, CheckpointMaintenance, CheckpointConnectionRequest) (CheckpointConnectionClosure, error)
	ObserveCheckpointConnectionClosure(context.Context, RestoreSourceDefinition, CheckpointMaintenance, CheckpointConnectionRequest) (CheckpointConnectionClosure, error)
}

func (s *Service) CloseCheckpointConnections(ctx context.Context, d RestoreSourceDefinition, m CheckpointMaintenance, r CheckpointConnectionRequest) (CheckpointConnectionClosure, error) {
	return s.checkpointConnectionClosure(ctx, d, m, r, true)
}

func (s *Service) ObserveCheckpointConnectionClosure(ctx context.Context, d RestoreSourceDefinition, m CheckpointMaintenance, r CheckpointConnectionRequest) (CheckpointConnectionClosure, error) {
	return s.checkpointConnectionClosure(ctx, d, m, r, false)
}

func (s *Service) checkpointConnectionClosure(ctx context.Context, d RestoreSourceDefinition, m CheckpointMaintenance, r CheckpointConnectionRequest, closeAdmission bool) (CheckpointConnectionClosure, error) {
	if err := ctx.Err(); err != nil {
		return CheckpointConnectionClosure{}, err
	}
	if r.Validate() != nil || !ValidCheckpointConnectionRecovery(m, r.CheckpointConnectionIdentity) || r.SourceResourceID != d.DataResourceID {
		return CheckpointConnectionClosure{}, ErrInvalid
	}
	backend, err := s.checkpointBackend(d)
	if err != nil {
		return CheckpointConnectionClosure{}, err
	}
	provider, ok := backend.Provider.(CheckpointConnectionClosureProvider)
	if !ok {
		return CheckpointConnectionClosure{}, ErrUnsupported
	}
	// Isolate caller selection from a provider retaining/mutating its request.
	expected := r
	expected.DatabaseNames = slices.Clone(r.DatabaseNames)
	r.DatabaseNames = slices.Clone(r.DatabaseNames)
	providerCtx, cancel := context.WithTimeout(ctx, s.providerTimeout)
	defer cancel()
	var actual CheckpointConnectionClosure
	if closeAdmission {
		actual, err = provider.CloseCheckpointConnections(providerCtx, d, m, r)
	} else {
		actual, err = provider.ObserveCheckpointConnectionClosure(providerCtx, d, m, r)
	}
	if err == nil {
		err = providerCtx.Err()
	}
	if err == nil {
		err = actual.Validate(expected)
	}
	if err != nil {
		return CheckpointConnectionClosure{}, err
	}
	actual.Databases = slices.Clone(actual.Databases)
	actual.ClosedAt = actual.ClosedAt.UTC()
	if err := providerCtx.Err(); err != nil {
		return CheckpointConnectionClosure{}, err
	}
	return actual, nil
}
