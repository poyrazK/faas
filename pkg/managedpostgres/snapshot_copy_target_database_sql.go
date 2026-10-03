package managedpostgres

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
)

// Target comes from a durably retained private target descriptor. Preparation pins the
// independently owned project and original adopted capture. Neither request
// shape nor an SQL borrow grants durable dispatch or dataset readiness.
type SnapshotCopyTargetDatabaseSQLRequest struct {
	Preparation SnapshotCopyTargetRequest
	Target      copyarchive.RestoreTarget
}

func (r SnapshotCopyTargetDatabaseSQLRequest) Validate(d RestoreSourceDefinition) error {
	x, s, p := r.Target, r.Target.Scope, r.Preparation
	if p.Validate() != nil || p.ExpectedProviderResourceID == "" || x.Validate() != nil {
		return ErrInvalid
	}
	if x.OwnerID != p.ResourceID || x.ProviderResourceID != p.ExpectedProviderResourceID || !x.ProviderCreatedAt.Equal(p.ExpectedCreatedAt) ||
		s.PostgresMajor != d.Spec.PostgresMajor || s.BackendID != d.BackendID || s.BackendFingerprint != d.BackendFingerprint ||
		s.SourceProviderResourceID != d.ProviderResourceID || s.SourceDataResourceID != d.DataResourceID || s.SourceDataResourceID != p.Capture.Snapshot.SourceResourceID ||
		s.ProviderSnapshotID != p.Capture.ProviderSnapshotID || s.CaptureProviderResourceID != p.Capture.ExpectedTargetResourceID ||
		!s.CapturePoint.Equal(p.Capture.Snapshot.PointInTime) || !s.SnapshotCreatedAt.Equal(p.SnapshotCreatedAt) || !s.CaptureCreatedAt.Equal(p.CaptureCreatedAt) {
		return ErrConflict
	}
	return nil
}

// OIDs are authenticated within the target cluster and may numerically equal
// source OIDs. Provider placement supplies physical isolation independently.
type SnapshotCopyTargetSQLIdentity struct {
	PostgresMajor          int
	DatabaseName, RoleName string
	DatabaseOID, RoleOID   uint32
}

func (SnapshotCopyTargetSQLIdentity) String() string     { return "private target PostgreSQL identity" }
func (i SnapshotCopyTargetSQLIdentity) GoString() string { return i.String() }
func (SnapshotCopyTargetSQLIdentity) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct{ PrivateTargetSQLIdentity bool }{true})
}

type SnapshotCopyTargetSQLRun func(context.Context, *pgx.Conn, SnapshotCopyTargetSQLIdentity) error

type SnapshotCopyTargetDatabaseSQLProvider interface {
	// The provider owns establishment, SQL/placement authentication before and
	// after the synchronous callback, and closure. The callback cannot retain it.
	WithSnapshotCopyTargetDatabaseSQL(context.Context, RestoreSourceDefinition, SnapshotCopyTargetDatabaseSQLRequest, SnapshotCopyTargetSQLRun) error
}

// Borrowing an already owned target remains independent of creation rollout or
// new account admission. The trusted worker must hold fresh mutation ownership.
func (s *Service) WithSnapshotCopyTargetDatabaseSQL(ctx context.Context, d RestoreSourceDefinition, r SnapshotCopyTargetDatabaseSQLRequest, run SnapshotCopyTargetSQLRun) error {
	if run == nil {
		return ErrInvalid
	}
	if err := r.Validate(d); err != nil {
		return err
	}
	b, err := s.checkpointBackend(d)
	if err != nil {
		return err
	}
	p, ok := b.Provider.(SnapshotCopyTargetDatabaseSQLProvider)
	if !ok {
		return ErrUnsupported
	}
	providerCtx, cancel := context.WithTimeout(ctx, s.providerTimeout)
	defer cancel()
	var mu sync.Mutex
	called, completed, returned := false, false, false
	var callbackErr, protocolErr error
	err = p.WithSnapshotCopyTargetDatabaseSQL(providerCtx, d, r, func(_ context.Context, conn *pgx.Conn, i SnapshotCopyTargetSQLIdentity) error {
		mu.Lock()
		if called || returned {
			protocolErr = ErrConflict
			mu.Unlock()
			return ErrConflict
		}
		called = true
		mu.Unlock()
		var runErr error
		defer func() {
			mu.Lock()
			callbackErr, completed = runErr, true
			mu.Unlock()
		}()
		t := r.Target
		if conn == nil || i.PostgresMajor != d.Spec.PostgresMajor || i.DatabaseName != t.DatabaseName || i.DatabaseOID != t.DatabaseOID || i.RoleName != t.RoleName || i.RoleOID != t.RoleOID {
			runErr = ErrConflict
			return runErr
		}
		if err := providerCtx.Err(); err != nil {
			runErr = err
			return runErr
		}
		runErr = run(providerCtx, conn, i)
		return runErr
	})
	mu.Lock()
	returned = true
	firstErr, violation, finished := callbackErr, protocolErr, called && completed
	mu.Unlock()
	if violation != nil {
		return violation
	}
	if firstErr != nil {
		return firstErr
	}
	if err == nil && !finished {
		return ErrConflict
	}
	if err == nil {
		return providerCtx.Err()
	}
	return err
}
