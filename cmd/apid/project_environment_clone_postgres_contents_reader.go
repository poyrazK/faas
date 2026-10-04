package main

import (
	"context"
	"reflect"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copycontents"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/state"
)

// Private composition: reserve/recover the original manifest before borrowing
// its retained reader. Recovery needs neither provider SQL nor a spool directory.
// Public clone readiness still requires the complete capture/import proofs.
func (s *server) projectEnvironmentClonePostgresContentsFromReader(ctx context.Context, l state.ProjectEnvironmentCloneLease,
	source capturedProjectEnvironmentDatabasePlan, exports copyinventory.ExportPlan, oid uint32, reserveBytes int64,
	limits state.ProjectEnvironmentClonePostgresContentsLimits, cfg copycontents.Config) (copycontents.Manifest, error) {
	return s.projectEnvironmentClonePostgresContents(ctx, l, exports, oid, reserveBytes, limits,
		func(ctx context.Context, d copyinventory.DatabaseExport, key [32]byte) (copycontents.Manifest, error) {
			cfg.Key = key
			return s.captureProjectEnvironmentClonePostgresContentsFromReader(ctx, l, source, d, cfg)
		})
}

func (s *server) captureProjectEnvironmentClonePostgresContentsFromReader(ctx context.Context, l state.ProjectEnvironmentCloneLease,
	source capturedProjectEnvironmentDatabasePlan, d copyinventory.DatabaseExport, cfg copycontents.Config) (copycontents.Manifest, error) {
	contents, ok := s.store.(state.ProjectEnvironmentClonePostgresContentsStore)
	if !ok || s.managedPostgres == nil {
		return copycontents.Manifest{}, managedpostgres.ErrUnavailable
	}
	if l.Operation.Status != state.CloneOperationCapturing || !clonePostgresInventoryMatchesSource(l, source, d.Scope) {
		return copycontents.Manifest{}, managedpostgres.ErrConflict
	}
	owner, err := contents.ProjectEnvironmentClonePostgresContentsForLease(ctx, l, d.Scope.SourceDatabaseID, d.Database.OID)
	if err != nil {
		return copycontents.Manifest{}, err
	}
	request, err := s.projectEnvironmentClonePostgresContentsReaderRequest(ctx, l, source, d, owner)
	if err != nil {
		return copycontents.Manifest{}, err
	}
	// Provider IO may take long enough for a handoff or input retirement. Check
	// the exact durable request both before and after every placement lookup.
	check := func(ctx context.Context) error {
		before, err := s.projectEnvironmentClonePostgresContentsReaderRequest(ctx, l, source, d, owner)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(before, request) {
			return managedpostgres.ErrConflict
		}
		actual, err := s.managedPostgres.FindSnapshotCopyReader(ctx, clonePostgresSnapshotDefinition(source), request.Reader)
		if err != nil {
			return err
		}
		if !actual.Available {
			return managedpostgres.ErrUnavailable
		}
		after, err := s.projectEnvironmentClonePostgresContentsReaderRequest(ctx, l, source, d, owner)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(after, request) {
			return managedpostgres.ErrConflict
		}
		return ctx.Err()
	}
	if err = check(ctx); err != nil {
		return copycontents.Manifest{}, err
	}
	var manifest copycontents.Manifest
	err = s.managedPostgres.WithSnapshotCopyReaderDatabaseSQL(ctx, clonePostgresSnapshotDefinition(source), request,
		func(sqlCtx context.Context, conn *pgx.Conn, _ managedpostgres.SnapshotCopyReaderSQLIdentity) error {
			var err error
			manifest, err = copycontents.Capture(sqlCtx, conn, d, cfg,
				func(ctx context.Context, borrowed *pgx.Conn, original copyinventory.DatabaseExport) error {
					if borrowed != conn || !reflect.DeepEqual(original, d) {
						return managedpostgres.ErrConflict
					}
					return check(ctx)
				})
			return err
		})
	// WithSnapshotCopyReaderDatabaseSQL authenticates the SQL identity and
	// provider placement again after Capture has rolled back its read transaction.
	// No completed manifest escapes if those checks or the final lease check fail.
	if err == nil {
		err = check(ctx)
	}
	if err != nil {
		return copycontents.Manifest{}, err
	}
	return manifest, nil
}

func (s *server) projectEnvironmentClonePostgresContentsReaderRequest(ctx context.Context, l state.ProjectEnvironmentCloneLease,
	source capturedProjectEnvironmentDatabasePlan, d copyinventory.DatabaseExport, original state.ProjectEnvironmentClonePostgresContents) (managedpostgres.SnapshotCopyReaderDatabaseSQLRequest, error) {
	var zero managedpostgres.SnapshotCopyReaderDatabaseSQLRequest
	contents, contentsOK := s.store.(state.ProjectEnvironmentClonePostgresContentsStore)
	readers, readerOK := s.store.(state.ProjectEnvironmentClonePostgresCopyReaderStore)
	snapshots, snapshotOK := s.store.(state.ProjectEnvironmentClonePostgresSnapshotStore)
	captures, captureOK := s.store.(state.ProjectEnvironmentClonePostgresSnapshotRestoreStore)
	if !contentsOK || !readerOK || !snapshotOK || !captureOK {
		return zero, managedpostgres.ErrUnavailable
	}
	owner, err := contents.ProjectEnvironmentClonePostgresContentsForLease(ctx, l, d.Scope.SourceDatabaseID, d.Database.OID)
	if err != nil {
		return zero, err
	}
	if owner.State != "reserved" || owner.OwnerID != original.OwnerID || !owner.Scope.Equal(d.Scope) || owner.DatabaseOID != d.Database.OID ||
		owner.InventoryFingerprint != d.InventoryFingerprint || owner.InventoryCiphertextSHA256 != original.InventoryCiphertextSHA256 ||
		owner.ArchiveOwnerID != original.ArchiveOwnerID || owner.ArchiveReservationSHA256 != original.ArchiveReservationSHA256 ||
		owner.ReaderOwnerID != original.ReaderOwnerID || owner.ReaderIdentitySHA256 != original.ReaderIdentitySHA256 ||
		owner.KeyID != original.KeyID || owner.ReservedBytes != original.ReservedBytes || !owner.CreatedAt.Equal(original.CreatedAt) {
		return zero, managedpostgres.ErrConflict
	}
	reader, err := readers.ProjectEnvironmentClonePostgresCopyReaderForLease(ctx, l, d.Scope.SourceDatabaseID)
	if err != nil {
		return zero, err
	}
	if reader.State != "observed" || !reader.Available || reader.OwnerID != owner.ReaderOwnerID ||
		!reader.Scope.Equal(d.Scope) || reader.IdentityFingerprint() != owner.ReaderIdentitySHA256 {
		return zero, managedpostgres.ErrConflict
	}
	snapshot, err := snapshots.ProjectEnvironmentClonePostgresSnapshotForLease(ctx, l, d.Scope.SourceDatabaseID)
	if err == nil {
		err = validateClonePostgresSnapshotPlan(l.Operation, source, snapshot)
	}
	if err != nil {
		return zero, err
	}
	capture, err := captures.ProjectEnvironmentClonePostgresSnapshotRestoreForLease(ctx, l, d.Scope.SourceDatabaseID)
	if err != nil {
		return zero, err
	}
	if snapshot.State != "retained" || capture.State != "adopted" || capture.AdoptedDatabaseID != d.Scope.CaptureDatabaseID {
		return zero, managedpostgres.ErrConflict
	}
	request := managedpostgres.SnapshotCopyReaderDatabaseSQLRequest{Reader: clonePostgresCopyReaderRequest(reader, snapshot, capture), Database: d}
	if err := request.Validate(clonePostgresSnapshotDefinition(source)); err != nil {
		return zero, err
	}
	return request, nil
}
