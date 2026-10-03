package managedpostgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
)

// SQL identity is observed on an independently authenticated owned reader. OIDs
// detect replacement within that capture; they are not target database IDs.
type SnapshotCopyReaderSQLIdentity struct {
	PostgresMajor          int
	DatabaseName, RoleName string
	DatabaseOID, RoleOID   uint32
}

type SnapshotCopyReaderSQLRead func(context.Context, *pgx.Conn, SnapshotCopyReaderSQLIdentity) error

type SnapshotCopyReaderSQLProvider interface {
	// The provider owns connection establishment, authentication and close.
	// The trusted callback borrows it synchronously and may not retain it.
	WithSnapshotCopyReaderSQL(context.Context, RestoreSourceDefinition, SnapshotCopyReaderRequest, SnapshotCopyReaderSQLRead) error
}

// Recovery reads an already owned, pinned reader even when creation is disabled.
// Connection configuration is neither returned nor persisted by this seam.
// No data-copy or readiness proof is made.
func (s *Service) WithSnapshotCopyReaderSQL(ctx context.Context, d RestoreSourceDefinition, r SnapshotCopyReaderRequest, read SnapshotCopyReaderSQLRead) error {
	return s.withSnapshotCopyReaderSQL(ctx, d, r, nil, read)
}

func (s *Service) withSnapshotCopyReaderSQL(ctx context.Context, d RestoreSourceDefinition, r SnapshotCopyReaderRequest, selected *copyinventory.DatabaseExport, read SnapshotCopyReaderSQLRead) error {
	if read == nil || r.Validate() != nil || r.ExpectedEndpointID == "" ||
		r.Capture.Snapshot.SourceResourceID != d.DataResourceID || r.ExpectedEndpointID == d.ProviderResourceID {
		return ErrInvalid
	}
	b, err := s.checkpointBackend(d)
	if err != nil {
		return err
	}
	var invoke func(context.Context, SnapshotCopyReaderSQLRead) error
	if selected == nil {
		p, ok := b.Provider.(SnapshotCopyReaderSQLProvider)
		if !ok {
			return ErrUnsupported
		}
		invoke = func(ctx context.Context, read SnapshotCopyReaderSQLRead) error {
			return p.WithSnapshotCopyReaderSQL(ctx, d, r, read)
		}
	} else {
		p, ok := b.Provider.(SnapshotCopyReaderDatabaseSQLProvider)
		if !ok {
			return ErrUnsupported
		}
		invoke = func(ctx context.Context, read SnapshotCopyReaderSQLRead) error {
			return p.WithSnapshotCopyReaderDatabaseSQL(ctx, d, SnapshotCopyReaderDatabaseSQLRequest{Reader: r, Database: *selected}, read)
		}
	}
	providerCtx, cancel := context.WithTimeout(ctx, s.providerTimeout)
	defer cancel()
	called := false
	var callbackErr error
	err = invoke(providerCtx, func(_ context.Context, conn *pgx.Conn, identity SnapshotCopyReaderSQLIdentity) error {
		if called {
			callbackErr = ErrConflict
			return callbackErr
		}
		called = true
		validDatabase := validOpaqueID(identity.DatabaseName)
		if selected != nil {
			validDatabase = identity.DatabaseName == selected.Database.Name && identity.DatabaseOID == selected.Database.OID && identity.RoleOID == selected.AuthenticatedReaderRoleOID
		}
		if conn == nil || identity.PostgresMajor != d.Spec.PostgresMajor ||
			!validDatabase || !validOpaqueID(identity.RoleName) || identity.DatabaseOID == 0 || identity.RoleOID == 0 {
			callbackErr = ErrConflict
			return callbackErr
		}
		if err := providerCtx.Err(); err != nil {
			callbackErr = err
			return callbackErr
		}
		// The caller's deadline remains authoritative even if a provider
		// supplies a callback context that outlives its connection budget.
		callbackErr = read(providerCtx, conn, identity)
		return callbackErr
	})
	if callbackErr != nil {
		return callbackErr
	}
	if err == nil && !called {
		return ErrConflict
	}
	if err == nil {
		return providerCtx.Err()
	}
	return err
}
