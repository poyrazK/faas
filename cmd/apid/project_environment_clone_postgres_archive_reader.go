package main

import (
	"context"
	"io"

	"filippo.io/age"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/state"
)

// Private composition of durable archive ownership and the already owned SQL
// reader. Recovery never invokes this producer or reconnects to today's source.
// Complete import/global coverage and public clone readiness remain separate.
func (s *server) projectEnvironmentClonePostgresArchiveFromReader(ctx context.Context, lease state.ProjectEnvironmentCloneLease, source capturedProjectEnvironmentDatabasePlan,
	exports copyinventory.ExportPlan, oid uint32, artifact clonePostgresArchiveStorage, reserveBytes int64, limits state.ProjectEnvironmentClonePostgresArchiveLimits,
	pgDump string, maxPlainBytes int64) (copyarchive.Receipt, error) {
	return s.projectEnvironmentClonePostgresArchive(ctx, lease, exports, oid, artifact, reserveBytes, limits,
		func(ctx context.Context, d copyinventory.DatabaseExport, key *age.X25519Recipient, w io.Writer) (copyarchive.Receipt, error) {
			return s.exportProjectEnvironmentClonePostgresArchiveFromReader(ctx, lease, source, d, pgDump, maxPlainBytes, key, w)
		})
}

func (s *server) exportProjectEnvironmentClonePostgresArchiveFromReader(ctx context.Context, lease state.ProjectEnvironmentCloneLease, plan capturedProjectEnvironmentDatabasePlan,
	d copyinventory.DatabaseExport, pgDump string, maxPlainBytes int64, key *age.X25519Recipient, w io.Writer) (copyarchive.Receipt, error) {
	readers, readerOK := s.store.(state.ProjectEnvironmentClonePostgresCopyReaderStore)
	snapshots, snapshotOK := s.store.(state.ProjectEnvironmentClonePostgresSnapshotStore)
	captures, captureOK := s.store.(state.ProjectEnvironmentClonePostgresSnapshotRestoreStore)
	if !readerOK || !snapshotOK || !captureOK || s.managedPostgres == nil {
		return copyarchive.Receipt{}, managedpostgres.ErrUnavailable
	}
	if d.Scope.SourceDatabaseID != plan.source.ID || d.Scope.OperationID != lease.Operation.ID || d.Scope.SourceVersion != plan.hash {
		return copyarchive.Receipt{}, managedpostgres.ErrConflict
	}
	reader, err := readers.ProjectEnvironmentClonePostgresCopyReaderForLease(ctx, lease, plan.source.ID)
	if err != nil {
		return copyarchive.Receipt{}, err
	}
	if reader.State != "observed" || !reader.Available || reader.EndpointID == "" || !reader.Scope.Equal(d.Scope) {
		return copyarchive.Receipt{}, managedpostgres.ErrConflict
	}
	snapshot, err := snapshots.ProjectEnvironmentClonePostgresSnapshotForLease(ctx, lease, plan.source.ID)
	if err == nil {
		err = validateClonePostgresSnapshotPlan(lease.Operation, plan, snapshot)
	}
	if err == nil && snapshot.State != "retained" {
		err = state.ErrConflict
	}
	if err != nil {
		return copyarchive.Receipt{}, err
	}
	capture, err := captures.ProjectEnvironmentClonePostgresSnapshotRestoreForLease(ctx, lease, plan.source.ID)
	if err != nil {
		return copyarchive.Receipt{}, err
	}
	if capture.State != "adopted" || capture.AdoptedDatabaseID != d.Scope.CaptureDatabaseID || capture.TargetProviderResourceID != d.Scope.CaptureProviderResourceID ||
		!capture.TargetCreatedAt.Equal(d.Scope.CaptureCreatedAt) {
		return copyarchive.Receipt{}, state.ErrConflict
	}
	request := managedpostgres.SnapshotCopyReaderDatabaseSQLRequest{Reader: clonePostgresCopyReaderRequest(reader, snapshot, capture), Database: d}
	var receipt copyarchive.Receipt
	err = s.managedPostgres.WithSnapshotCopyReaderDatabaseSQL(ctx, clonePostgresSnapshotDefinition(plan), request,
		func(sqlCtx context.Context, conn *pgx.Conn, _ managedpostgres.SnapshotCopyReaderSQLIdentity) error {
			var err error
			receipt, err = copyarchive.Export(sqlCtx, conn, d, pgDump, key, w, maxPlainBytes)
			return err
		})
	// The producer succeeds only after the provider's post-dump SQL and
	// placement checks. Failure closes the upload pipe with an error, even
	// when the dump itself finished, preventing atomic artifact publication.
	if err != nil {
		return copyarchive.Receipt{}, err
	}
	return receipt, nil
}
