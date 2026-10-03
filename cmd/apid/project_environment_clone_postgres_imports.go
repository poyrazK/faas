package main

import (
	"context"
	"errors"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/state"
)

// Private single-database composition. All SQL identity comes from the first
// retained child preparation and complete plan. The target argument is only an
// exact assertion; it cannot choose a database. Executed is a command receipt,
// not dataset/stage readiness. Unknown imports recover their window close-only.
func (s *server) projectEnvironmentClonePostgresImport(ctx context.Context, lease state.ProjectEnvironmentCloneLease, source capturedProjectEnvironmentDatabasePlan,
	exports copyinventory.ExportPlan, oid uint32, target copyarchive.RestoreTarget, artifact clonePostgresArchiveStorage, pgRestore, scratchRoot string, maxPlainBytes int64) (copyarchive.RestoreExecution, error) {
	var zero copyarchive.RestoreExecution
	archives, archiveOK := s.store.(state.ProjectEnvironmentClonePostgresArchiveStore)
	imports, importOK := s.store.(state.ProjectEnvironmentClonePostgresImportStore)
	if !archiveOK || !importOK || mfaIdentities == nil {
		return zero, managedpostgres.ErrUnavailable
	}
	ctx, cancel := context.WithDeadline(ctx, lease.ExpiresAt)
	defer cancel()
	requirements, err := exports.RequirementsForWorker()
	if err != nil {
		return zero, err
	}
	var d copyinventory.DatabaseExport
	for _, selected := range requirements {
		if selected.Database.OID == oid {
			d = selected
		}
	}
	if d.Database.OID == 0 || !clonePostgresInventoryMatchesSource(lease, source, d.Scope) || !target.Scope.Equal(d.Scope) {
		return zero, managedpostgres.ErrConflict
	}
	a, err := archives.ProjectEnvironmentClonePostgresArchiveForLease(ctx, lease, d.Scope.SourceDatabaseID, oid)
	if err != nil {
		return zero, err
	}
	if a.State != "retained" || !a.Scope.Equal(d.Scope) || a.InventoryFingerprint != d.InventoryFingerprint || a.StorageID != artifact.ID || a.StorageFingerprint != artifact.Fingerprint {
		return zero, managedpostgres.ErrConflict
	}
	// Metadata recovery never creates a parent, prepares a new database, or checks
	// the closed catalogue before the original uncertain window can be quiesced.
	prepared, err := s.openProjectEnvironmentClonePostgresDatabasePreparation(ctx, lease, source, exports, oid)
	if err != nil {
		return zero, err
	}
	actual, err := prepared.receipt.TargetForWorker()
	if err != nil {
		return zero, err
	}
	if actual != target || prepared.owner.ArchiveOwnerID != a.OwnerID || prepared.owner.ArchiveReservationSHA256 != a.ReservationFingerprint() {
		return zero, managedpostgres.ErrConflict
	}
	owner, _, err := imports.ReserveProjectEnvironmentClonePostgresImport(ctx, lease, state.ProjectEnvironmentClonePostgresImportRequest{
		Input: a.Receipt, Target: actual, DatabaseSQLPinsCiphertextSHA256: prepared.owner.Sealed.CiphertextSHA256,
		DatabasePlanCiphertextSHA256: prepared.owner.DatabasePlanCiphertextSHA256, ArchiveReservationSHA256: prepared.owner.ArchiveReservationSHA256})
	if err != nil {
		return zero, err
	}
	if !owner.MatchesDatabaseSQLPins(prepared.owner) || s.managedPostgres == nil {
		return zero, managedpostgres.ErrUnavailable
	}
	dispatch, err := uuid.Parse(owner.ImportID)
	if err != nil || dispatch == uuid.Nil {
		return zero, managedpostgres.ErrConflict
	}
	pins := s.store.(state.ProjectEnvironmentClonePostgresDatabaseSQLPinsStore)
	authorize := func(checkCtx context.Context, got copyarchive.RestoreTarget) error {
		fresh, err := imports.ProjectEnvironmentClonePostgresImportForLease(checkCtx, lease, d.Scope.SourceDatabaseID, oid)
		if err != nil {
			return err
		}
		child, err := pins.ProjectEnvironmentClonePostgresDatabaseSQLPinsForLease(checkCtx, lease, d.Scope.SourceDatabaseID, oid)
		if err == nil && (got != prepared.bootstrap || fresh != owner || !fresh.MatchesDatabaseSQLPins(child) || !sameClonePostgresDatabaseSQLPins(child, prepared.owner)) {
			err = managedpostgres.ErrConflict
		}
		return err
	}
	if err = authorize(ctx, prepared.bootstrap); err != nil {
		return zero, err
	}
	bootstrapRequest, err := s.projectEnvironmentClonePostgresTargetDatabaseSQLRequest(ctx, lease, source, d.Scope, prepared.bootstrap)
	if err != nil {
		return zero, err
	}
	if owner.State != "reserved" {
		// Neither an uncertain attempt nor a recorded command may rely on metadata
		// alone. Close/verify the exact original window under provider pre/postchecks.
		err = s.managedPostgres.WithSnapshotCopyTargetDatabaseSQL(ctx, clonePostgresSnapshotDefinition(source), bootstrapRequest,
			func(sqlCtx context.Context, conn *pgx.Conn, _ managedpostgres.SnapshotCopyTargetSQLIdentity) error {
				closed, err := prepared.receipt.CloseMaintenance(sqlCtx, conn, exports, dispatch, authorize)
				if err == nil && closed.ClosedAt().IsZero() {
					err = managedpostgres.ErrConflict
				}
				return err
			})
		if err == nil {
			err = authorize(ctx, prepared.bootstrap)
		}
		if owner.State != "executed" {
			return zero, errors.Join(managedpostgres.ErrUnavailable, err)
		}
		if err != nil {
			return zero, err
		}
		return copyarchive.RestoreExecution{Input: owner.Input, Target: actual}, nil
	}
	if artifact.Backend == nil {
		return zero, managedpostgres.ErrUnavailable
	}
	var identities []*age.X25519Identity
	for _, id := range mfaIdentities() {
		if id != nil && id.Recipient().String() == a.KeyID {
			identities = append(identities, id)
		}
	}
	if len(identities) == 0 {
		return zero, managedpostgres.ErrUnavailable
	}
	childRequest, err := s.projectEnvironmentClonePostgresTargetDatabaseSQLRequest(ctx, lease, source, d.Scope, actual)
	if err != nil {
		return zero, err
	}
	staged, err := copyarchive.StageRetained(ctx, clonePostgresArchiveBackend{artifact.Backend}, a.StorageKey, d, identities, a.Receipt, scratchRoot, maxPlainBytes, a.ReservedBytes)
	if err != nil {
		return zero, err
	}
	defer func() { _ = staged.Close() }()
	var execution copyarchive.RestoreExecution
	err = s.managedPostgres.WithSnapshotCopyTargetDatabaseSQL(ctx, clonePostgresSnapshotDefinition(source), bootstrapRequest,
		func(sqlCtx context.Context, conn *pgx.Conn, _ managedpostgres.SnapshotCopyTargetSQLIdentity) error {
			if err := prepared.receipt.VerifyForWorker(sqlCtx, conn, exports, authorize); err != nil {
				return err
			}
			claimed, first, err := imports.ClaimProjectEnvironmentClonePostgresImport(sqlCtx, lease, d.Scope.SourceDatabaseID, oid)
			if err != nil {
				return err
			}
			if !first || claimed.State != "importing" || claimed.ImportID != owner.ImportID || !claimed.CreatedAt.Equal(owner.CreatedAt) || !claimed.MatchesDatabaseSQLPins(prepared.owner) {
				return managedpostgres.ErrUnavailable
			}
			// Commit the sole dispatch claim before SQL can open admission. An unknown
			// claim reply returns above and never authorizes a maintenance callback.
			owner = claimed
			closed, err := prepared.receipt.WithMaintenance(sqlCtx, conn, exports, dispatch, authorize,
				func(runCtx context.Context, child copyarchive.RestoreTarget) error {
					if child != actual {
						return managedpostgres.ErrConflict
					}
					return s.managedPostgres.WithSnapshotCopyTargetDatabaseSQL(runCtx, clonePostgresSnapshotDefinition(source), childRequest,
						func(restoreCtx context.Context, selected *pgx.Conn, _ managedpostgres.SnapshotCopyTargetSQLIdentity) error {
							placement := func(checkCtx context.Context, got *pgx.Conn, t copyarchive.RestoreTarget) error {
								if got != selected || t != actual {
									return managedpostgres.ErrConflict
								}
								return authorize(checkCtx, prepared.bootstrap)
							}
							var err error
							execution, err = staged.Restore(restoreCtx, selected, child, pgRestore, placement)
							return err
						})
				})
			if err == nil && closed.ClosedAt().IsZero() {
				err = managedpostgres.ErrConflict
			}
			return err
		})
	if err != nil {
		return zero, err
	}
	if err = authorize(ctx, prepared.bootstrap); err != nil {
		return zero, err
	}
	if !copyarchive.SameReceipt(execution.Input, a.Receipt) || execution.Target != actual {
		return zero, managedpostgres.ErrConflict
	}
	if _, err = imports.RecordProjectEnvironmentClonePostgresImportExecution(ctx, lease, d.Scope.SourceDatabaseID, oid, execution); err != nil {
		return zero, err
	}
	return execution, nil
}

func (s *server) projectEnvironmentClonePostgresTargetDatabaseSQLRequest(ctx context.Context, lease state.ProjectEnvironmentCloneLease, source capturedProjectEnvironmentDatabasePlan,
	scope copyinventory.Scope, target copyarchive.RestoreTarget) (managedpostgres.SnapshotCopyTargetDatabaseSQLRequest, error) {
	preparation, actual, err := s.projectEnvironmentClonePostgresTargetSQLPreparation(ctx, lease, source, scope)
	if err != nil {
		return managedpostgres.SnapshotCopyTargetDatabaseSQLRequest{}, err
	}
	if actual.TargetDatabaseID != target.OwnerID || actual.ProviderResourceID != target.ProviderResourceID ||
		!actual.ProviderCreatedAt.Equal(target.ProviderCreatedAt) {
		return managedpostgres.SnapshotCopyTargetDatabaseSQLRequest{}, managedpostgres.ErrConflict
	}
	return managedpostgres.SnapshotCopyTargetDatabaseSQLRequest{Target: target, Preparation: preparation}, nil
}
