package main

import (
	"context"

	"filippo.io/age"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/state"
)

// Private single-database composition. The caller supplies an originally planned
// requirement, qualified target SQL pins and reserved local spool capacity. This
// is not installed in public clone admission and does not import globals or grant
// complete dataset readiness. Unknown SQL outcomes never repeat an import.
func (s *server) projectEnvironmentClonePostgresImport(ctx context.Context, lease state.ProjectEnvironmentCloneLease, source capturedProjectEnvironmentDatabasePlan,
	exports copyinventory.ExportPlan, oid uint32, target copyarchive.RestoreTarget, artifact clonePostgresArchiveStorage, pgRestore, scratchRoot string, maxPlainBytes int64) (copyarchive.RestoreExecution, error) {
	archives, archiveOK := s.store.(state.ProjectEnvironmentClonePostgresArchiveStore)
	imports, importOK := s.store.(state.ProjectEnvironmentClonePostgresImportStore)
	if !archiveOK || !importOK || artifact.Backend == nil || mfaIdentities == nil {
		return copyarchive.RestoreExecution{}, managedpostgres.ErrUnavailable
	}
	ctx, cancel := context.WithDeadline(ctx, lease.ExpiresAt)
	defer cancel()
	requirements, err := exports.RequirementsForWorker()
	if err != nil {
		return copyarchive.RestoreExecution{}, err
	}
	var d copyinventory.DatabaseExport
	for _, selected := range requirements {
		if selected.Database.OID == oid {
			d = selected
		}
	}
	if d.Database.OID == 0 || d.Scope.SourceDatabaseID != source.source.ID || d.Scope.OperationID != lease.Operation.ID || d.Scope.SourceVersion != source.hash || !target.Scope.Equal(d.Scope) {
		return copyarchive.RestoreExecution{}, managedpostgres.ErrConflict
	}
	a, err := archives.ProjectEnvironmentClonePostgresArchiveForLease(ctx, lease, d.Scope.SourceDatabaseID, oid)
	if err != nil {
		return copyarchive.RestoreExecution{}, err
	}
	if a.State != "retained" || !a.Scope.Equal(d.Scope) || a.InventoryFingerprint != d.InventoryFingerprint || a.StorageID != artifact.ID || a.StorageFingerprint != artifact.Fingerprint {
		return copyarchive.RestoreExecution{}, managedpostgres.ErrConflict
	}
	owner, _, err := imports.ReserveProjectEnvironmentClonePostgresImport(ctx, lease, state.ProjectEnvironmentClonePostgresImportRequest{Input: a.Receipt, Target: target})
	if err != nil {
		return copyarchive.RestoreExecution{}, err
	}
	if owner.State == "executed" {
		// Replay is the previously durable command result, not a current target
		// dataset/placement proof. Publication still needs independent verification.
		return copyarchive.RestoreExecution{Input: owner.Input, Target: target}, nil
	}
	if owner.State != "reserved" {
		return copyarchive.RestoreExecution{}, managedpostgres.ErrUnavailable
	}
	var identities []*age.X25519Identity
	for _, id := range mfaIdentities() {
		if id != nil && id.Recipient().String() == a.KeyID {
			identities = append(identities, id)
		}
	}
	if len(identities) == 0 || s.managedPostgres == nil {
		return copyarchive.RestoreExecution{}, managedpostgres.ErrUnavailable
	}
	request, err := s.projectEnvironmentClonePostgresImportSQLRequest(ctx, lease, source, d, target)
	if err != nil {
		return copyarchive.RestoreExecution{}, err
	}
	staged, err := copyarchive.StageRetained(ctx, clonePostgresArchiveBackend{artifact.Backend}, a.StorageKey, d, identities, a.Receipt, scratchRoot, maxPlainBytes, a.ReservedBytes)
	if err != nil {
		return copyarchive.RestoreExecution{}, err
	}
	defer func() { _ = staged.Close() }()
	var execution copyarchive.RestoreExecution
	err = s.managedPostgres.WithSnapshotCopyTargetDatabaseSQL(ctx, clonePostgresSnapshotDefinition(source), request,
		func(sqlCtx context.Context, conn *pgx.Conn, _ managedpostgres.SnapshotCopyTargetSQLIdentity) error {
			claimed, dispatch, err := imports.ClaimProjectEnvironmentClonePostgresImport(sqlCtx, lease, d.Scope.SourceDatabaseID, oid)
			if err != nil {
				return err
			}
			if !dispatch || claimed.ImportID != owner.ImportID || !claimed.MatchesTarget(target) {
				return managedpostgres.ErrUnavailable
			}
			// The encompassing provider borrow establishes physical placement
			// around the callback. The restore also rechecks the exact borrowed
			// connection, immutable descriptor and live durable lease at its
			// boundaries. Nothing is recorded until provider postchecks succeed.
			placement := func(checkCtx context.Context, got *pgx.Conn, actual copyarchive.RestoreTarget) error {
				fresh, err := imports.ProjectEnvironmentClonePostgresImportForLease(checkCtx, lease, d.Scope.SourceDatabaseID, oid)
				if err != nil {
					return err
				}
				if got != conn || fresh.State != "importing" || fresh.ImportID != owner.ImportID || !fresh.MatchesTarget(actual) {
					return managedpostgres.ErrConflict
				}
				return nil
			}
			execution, err = staged.Restore(sqlCtx, conn, target, pgRestore, placement)
			return err
		})
	if err != nil {
		return copyarchive.RestoreExecution{}, err
	}
	if !copyarchive.SameReceipt(execution.Input, a.Receipt) || !owner.MatchesTarget(execution.Target) {
		return copyarchive.RestoreExecution{}, managedpostgres.ErrConflict
	}
	_, err = imports.RecordProjectEnvironmentClonePostgresImportExecution(ctx, lease, d.Scope.SourceDatabaseID, oid, execution)
	if err != nil {
		return copyarchive.RestoreExecution{}, err
	}
	return execution, nil
}

func (s *server) projectEnvironmentClonePostgresImportSQLRequest(ctx context.Context, lease state.ProjectEnvironmentCloneLease, source capturedProjectEnvironmentDatabasePlan,
	d copyinventory.DatabaseExport, target copyarchive.RestoreTarget) (managedpostgres.SnapshotCopyTargetDatabaseSQLRequest, error) {
	snapshots, snapshotOK := s.store.(state.ProjectEnvironmentClonePostgresSnapshotStore)
	captures, captureOK := s.store.(state.ProjectEnvironmentClonePostgresSnapshotRestoreStore)
	targets, targetOK := s.store.(state.ProjectEnvironmentClonePostgresCopyTargetStore)
	if !snapshotOK || !captureOK || !targetOK {
		return managedpostgres.SnapshotCopyTargetDatabaseSQLRequest{}, managedpostgres.ErrUnavailable
	}
	snapshot, err := snapshots.ProjectEnvironmentClonePostgresSnapshotForLease(ctx, lease, source.source.ID)
	if err == nil {
		err = validateClonePostgresSnapshotPlan(lease.Operation, source, snapshot)
	}
	if err != nil {
		return managedpostgres.SnapshotCopyTargetDatabaseSQLRequest{}, err
	}
	capture, err := captures.ProjectEnvironmentClonePostgresSnapshotRestoreForLease(ctx, lease, source.source.ID)
	if err != nil {
		return managedpostgres.SnapshotCopyTargetDatabaseSQLRequest{}, err
	}
	actual, err := targets.ProjectEnvironmentClonePostgresCopyTargetForLease(ctx, lease, source.source.ID)
	if err != nil {
		return managedpostgres.SnapshotCopyTargetDatabaseSQLRequest{}, err
	}
	if snapshot.State != "retained" || capture.State != "adopted" || capture.AdoptedDatabaseID != d.Scope.CaptureDatabaseID || capture.TargetProviderResourceID != d.Scope.CaptureProviderResourceID ||
		!capture.TargetCreatedAt.Equal(d.Scope.CaptureCreatedAt) || actual.State != "prepared" || actual.TargetDatabaseID != target.OwnerID || actual.ProviderResourceID != target.ProviderResourceID ||
		!actual.ProviderCreatedAt.Equal(target.ProviderCreatedAt) {
		return managedpostgres.SnapshotCopyTargetDatabaseSQLRequest{}, managedpostgres.ErrConflict
	}
	return managedpostgres.SnapshotCopyTargetDatabaseSQLRequest{Target: target, Preparation: managedpostgres.SnapshotCopyTargetRequest{
		ResourceID: actual.TargetDatabaseID, ExpectedProviderResourceID: actual.ProviderResourceID, ExpectedCreatedAt: actual.ProviderCreatedAt,
		SnapshotCreatedAt: snapshot.SnapshotCreatedAt, CaptureCreatedAt: capture.TargetCreatedAt,
		Capture: managedpostgres.SnapshotRestoreRequest{ResourceID: capture.TargetOwnerID, ProviderSnapshotID: snapshot.ProviderSnapshotID,
			ExpectedTargetResourceID: capture.TargetProviderResourceID, Snapshot: clonePostgresSnapshotRequest(snapshot)}}}, nil
}
