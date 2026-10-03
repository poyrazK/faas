package state

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Operation/capture/target/catalogue and retained inventory/archive locks are
// held together. Live desired source changes cannot substitute a target/input.
func clonePostgresImportContextTx(ctx context.Context, tx pgx.Tx, lease ProjectEnvironmentCloneLease, sourceID string, oid uint32) (ProjectEnvironmentClonePostgresCopyTarget, ProjectEnvironmentClonePostgresArchive, error) {
	op, capture, source, resource, point, err := clonePostgresCopyContextTx(ctx, tx, lease, sourceID)
	if err != nil {
		return ProjectEnvironmentClonePostgresCopyTarget{}, ProjectEnvironmentClonePostgresArchive{}, err
	}
	target, err := readClonePostgresCopyTargetTx(ctx, tx, op, capture, source, resource, point)
	if err != nil {
		return target, ProjectEnvironmentClonePostgresArchive{}, err
	}
	scope, inventory, err := clonePostgresArchiveContextTx(ctx, tx, lease, sourceID)
	if err != nil {
		return target, ProjectEnvironmentClonePostgresArchive{}, err
	}
	archive, err := readClonePostgresArchiveTx(ctx, tx, scope, inventory.Sealed.Fingerprint, oid)
	if err == nil && (archive.State != "retained" || target.CaptureDatabaseID != scope.CaptureDatabaseID || target.ProviderResourceID == "" || target.ProviderCreatedAt.Before(scope.CaptureCreatedAt)) {
		err = ErrConflict
	}
	return target, archive, err
}

func readClonePostgresImportTx(ctx context.Context, tx pgx.Tx, lease ProjectEnvironmentCloneLease, target ProjectEnvironmentClonePostgresCopyTarget, a ProjectEnvironmentClonePostgresArchive) (ProjectEnvironmentClonePostgresImport, error) {
	r, err := new(sqlc.Queries).ReadProjectEnvironmentClonePostgresImport(ctx, tx, sqlc.ReadProjectEnvironmentClonePostgresImportParams{
		OperationID: mustPgUUID(a.Scope.OperationID), SourceDatabaseID: mustPgUUID(a.Scope.SourceDatabaseID), DatabaseOid: int64(a.DatabaseOID)})
	if err != nil {
		return ProjectEnvironmentClonePostgresImport{}, mapErr(err)
	}
	i := ProjectEnvironmentClonePostgresImport{ImportID: pgUUIDString(r.ImportID), State: r.State, ArchiveOwnerID: pgUUIDString(r.ArchiveOwnerID), TargetDatabaseID: pgUUIDString(r.TargetDatabaseID),
		TargetProviderResourceID: r.TargetProviderResourceID, TargetFingerprint: r.TargetFingerprint, TargetProviderCreatedAt: r.TargetProviderCreatedAt.Time,
		DatabaseSQLPinsCiphertextSHA256: r.DatabaseSqlPinsCiphertextSha256.String, DatabasePlanCiphertextSHA256: r.DatabasePlanCiphertextSha256.String, ArchiveReservationSHA256: r.ArchiveReservationSha256.String,
		CreatedAt: r.CreatedAt.Time, ImportStartedAt: r.ImportStartedAt.Time, ExecutedAt: r.ExecutedAt.Time, Input: a.Receipt}
	if !validCloneCredentialSourceID(i.ImportID) || i.ImportID == a.Scope.OperationID || i.ImportID == a.Scope.SourceDatabaseID || i.ImportID == a.OwnerID || i.ImportID == target.TargetDatabaseID ||
		i.ArchiveOwnerID != a.OwnerID || r.ArchiveCiphertextSha256 != a.Receipt.CiphertextSHA256 || pgUUIDString(r.AccountID) != a.Scope.AccountID || pgUUIDString(r.ProjectID) != a.Scope.ProjectID ||
		i.TargetDatabaseID != target.TargetDatabaseID || i.TargetProviderResourceID != target.ProviderResourceID || !i.TargetProviderCreatedAt.Equal(target.ProviderCreatedAt) ||
		!validCloneObjectSHA256(i.TargetFingerprint) || i.CreatedAt.Before(a.RetainedAt) || i.CreatedAt.Before(target.PreparedAt) || i.CreatedAt.Before(target.ProviderCreatedAt) ||
		!r.CreatedAt.Valid || !r.TargetProviderCreatedAt.Valid || r.CreatedAt.InfinityModifier != pgtype.Finite || r.TargetProviderCreatedAt.InfinityModifier != pgtype.Finite {
		return i, ErrConflict
	}
	if (i.State == "reserved" && (r.ImportStartedAt.Valid || r.ExecutedAt.Valid)) ||
		(i.State == "importing" && (!r.ImportStartedAt.Valid || r.ExecutedAt.Valid)) ||
		(i.State == "executed" && (!r.ImportStartedAt.Valid || !r.ExecutedAt.Valid)) ||
		(i.State != "reserved" && i.State != "importing" && i.State != "executed") ||
		(r.ImportStartedAt.Valid && (r.ImportStartedAt.InfinityModifier != pgtype.Finite || i.ImportStartedAt.Before(i.CreatedAt))) ||
		(r.ExecutedAt.Valid && (r.ExecutedAt.InfinityModifier != pgtype.Finite || i.ExecutedAt.Before(i.ImportStartedAt))) {
		return i, ErrConflict
	}
	if r.DatabaseSqlPinsCiphertextSha256.Valid || r.DatabasePlanCiphertextSha256.Valid || r.ArchiveReservationSha256.Valid {
		if !r.DatabaseSqlPinsCiphertextSha256.Valid || !r.DatabasePlanCiphertextSha256.Valid || !r.ArchiveReservationSha256.Valid {
			return i, ErrConflict
		}
		p, err := clonePostgresImportPreparationTx(ctx, tx, lease, a)
		if err != nil {
			return i, err
		}
		if !i.MatchesDatabaseSQLPins(p) || i.CreatedAt.Before(p.CapturedAt) {
			return i, ErrConflict
		}
	}
	return i, nil
}

// Read the child and all original prerequisites while the import transaction
// holds its operation/target/archive locks. A substituted parent cannot claim,
// complete or replay an already occupied import.
func clonePostgresImportPreparationTx(ctx context.Context, tx pgx.Tx, l ProjectEnvironmentCloneLease, a ProjectEnvironmentClonePostgresArchive) (ProjectEnvironmentClonePostgresDatabaseSQLPins, error) {
	plan, bootstrap, original, err := clonePostgresDatabaseSQLPinsContextTx(ctx, tx, l, a.Scope.SourceDatabaseID, a.DatabaseOID)
	if err != nil {
		return ProjectEnvironmentClonePostgresDatabaseSQLPins{}, err
	}
	if original.ReservationFingerprint() != a.ReservationFingerprint() || !copyarchive.SameReceipt(original.Receipt, a.Receipt) {
		return ProjectEnvironmentClonePostgresDatabaseSQLPins{}, ErrConflict
	}
	return readClonePostgresDatabaseSQLPinsTx(ctx, tx, plan, bootstrap, original)
}

func (s *PgStore) ReserveProjectEnvironmentClonePostgresImport(ctx context.Context, lease ProjectEnvironmentCloneLease, request ProjectEnvironmentClonePostgresImportRequest) (ProjectEnvironmentClonePostgresImport, bool, error) {
	fingerprint, err := request.Target.Fingerprint()
	if !validCloneLeaseIdentity(lease) || err != nil || request.Input.SourceDatabaseOID == 0 || request.Input.Scope.Validate() != nil || !request.bindingValid() {
		return ProjectEnvironmentClonePostgresImport{}, false, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentClonePostgresImport{}, false, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	target, a, err := clonePostgresImportContextTx(ctx, tx, lease, request.Input.Scope.SourceDatabaseID, request.Input.SourceDatabaseOID)
	if err != nil {
		return ProjectEnvironmentClonePostgresImport{}, false, err
	}
	if lease.Operation.Status != CloneOperationCapturing || target.State != "prepared" || !copyarchive.SameReceipt(a.Receipt, request.Input) ||
		request.Target.OwnerID != target.TargetDatabaseID || request.Target.ProviderResourceID != target.ProviderResourceID || !request.Target.ProviderCreatedAt.Equal(target.ProviderCreatedAt) ||
		!request.Target.Scope.Equal(a.Scope) {
		return ProjectEnvironmentClonePostgresImport{}, false, ErrConflict
	}
	if request.DatabaseSQLPinsCiphertextSHA256 != "" {
		p, err := clonePostgresImportPreparationTx(ctx, tx, lease, a)
		if err != nil {
			return ProjectEnvironmentClonePostgresImport{}, false, err
		}
		if p.Sealed.CiphertextSHA256 != request.DatabaseSQLPinsCiphertextSHA256 || p.DatabasePlanCiphertextSHA256 != request.DatabasePlanCiphertextSHA256 ||
			p.ArchiveReservationSHA256 != request.ArchiveReservationSHA256 || p.Sealed.Fingerprint != fingerprint {
			return ProjectEnvironmentClonePostgresImport{}, false, ErrConflict
		}
	}
	i, err := readClonePostgresImportTx(ctx, tx, lease, target, a)
	created := errors.Is(err, ErrNotFound)
	if created {
		// The archive PK bounds this to one import per already charged archive;
		// the independently charged target cannot be replaced on retry.
		_, err = new(sqlc.Queries).InsertProjectEnvironmentClonePostgresImport(ctx, tx, sqlc.InsertProjectEnvironmentClonePostgresImportParams{
			OperationID: mustPgUUID(a.Scope.OperationID), SourceDatabaseID: mustPgUUID(a.Scope.SourceDatabaseID), DatabaseOid: int64(a.DatabaseOID),
			AccountID: mustPgUUID(a.Scope.AccountID), ProjectID: mustPgUUID(a.Scope.ProjectID), ImportID: mustPgUUID(uuid.NewString()), ArchiveOwnerID: mustPgUUID(a.OwnerID),
			ArchiveCiphertextSha256: a.Receipt.CiphertextSHA256, TargetDatabaseID: mustPgUUID(target.TargetDatabaseID), TargetProviderResourceID: target.ProviderResourceID,
			TargetProviderCreatedAt: pgtype.Timestamptz{Time: target.ProviderCreatedAt, Valid: true}, TargetFingerprint: fingerprint,
			DatabaseSqlPinsCiphertextSha256: pgtype.Text{String: request.DatabaseSQLPinsCiphertextSHA256, Valid: request.DatabaseSQLPinsCiphertextSHA256 != ""},
			DatabasePlanCiphertextSha256:    pgtype.Text{String: request.DatabasePlanCiphertextSHA256, Valid: request.DatabasePlanCiphertextSHA256 != ""},
			ArchiveReservationSha256:        pgtype.Text{String: request.ArchiveReservationSHA256, Valid: request.ArchiveReservationSHA256 != ""},
			ExpectedRevision:                lease.Operation.Revision, WorkerToken: lease.Token})
		if err != nil {
			return i, false, cloneCopyReaderMutationError(err)
		}
		i, err = readClonePostgresImportTx(ctx, tx, lease, target, a)
	}
	if err != nil {
		return i, false, err
	}
	if !i.MatchesTarget(request.Target) || i.DatabaseSQLPinsCiphertextSHA256 != request.DatabaseSQLPinsCiphertextSHA256 ||
		i.DatabasePlanCiphertextSHA256 != request.DatabasePlanCiphertextSHA256 || i.ArchiveReservationSHA256 != request.ArchiveReservationSHA256 {
		return i, false, ErrConflict
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, lease.Operation, &lease); err != nil {
		return i, false, err
	}
	return i, created, mapErr(tx.Commit(ctx))
}

func (s *PgStore) mutateClonePostgresImport(ctx context.Context, lease ProjectEnvironmentCloneLease, sourceID string, oid uint32, action string, execution copyarchive.RestoreExecution) (ProjectEnvironmentClonePostgresImport, bool, error) {
	if !validCloneLeaseIdentity(lease) || !validCloneCredentialSourceID(sourceID) || oid == 0 {
		return ProjectEnvironmentClonePostgresImport{}, false, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentClonePostgresImport{}, false, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	target, a, err := clonePostgresImportContextTx(ctx, tx, lease, sourceID, oid)
	if err != nil {
		return ProjectEnvironmentClonePostgresImport{}, false, err
	}
	i, err := readClonePostgresImportTx(ctx, tx, lease, target, a)
	if err != nil {
		return i, false, err
	}
	if action != "read" && (lease.Operation.Status != CloneOperationCapturing || target.State != "prepared") {
		return i, false, ErrConflict
	}
	q, dispatch := new(sqlc.Queries), false
	switch action {
	case "read":
	case "claim":
		if i.State == "reserved" {
			_, err = q.ClaimProjectEnvironmentClonePostgresImport(ctx, tx, sqlc.ClaimProjectEnvironmentClonePostgresImportParams{
				OperationID: mustPgUUID(a.Scope.OperationID), SourceDatabaseID: mustPgUUID(sourceID), DatabaseOid: int64(oid), ExpectedRevision: lease.Operation.Revision, WorkerToken: lease.Token})
			dispatch = true
		}
	case "record":
		if i.State == "reserved" || !copyarchive.SameReceipt(i.Input, execution.Input) || !i.MatchesTarget(execution.Target) {
			return i, false, ErrConflict
		}
		if i.State == "importing" {
			_, err = q.RecordProjectEnvironmentClonePostgresImportExecution(ctx, tx, sqlc.RecordProjectEnvironmentClonePostgresImportExecutionParams{
				OperationID: mustPgUUID(a.Scope.OperationID), SourceDatabaseID: mustPgUUID(sourceID), DatabaseOid: int64(oid), ExpectedRevision: lease.Operation.Revision, WorkerToken: lease.Token})
		}
	default:
		return i, false, ErrInvalidArgument
	}
	if err != nil {
		return i, false, cloneCopyReaderMutationError(err)
	}
	i, err = readClonePostgresImportTx(ctx, tx, lease, target, a)
	if err != nil {
		return i, false, err
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, lease.Operation, &lease); err != nil {
		return i, false, err
	}
	return i, dispatch, mapErr(tx.Commit(ctx))
}

func (s *PgStore) ProjectEnvironmentClonePostgresImportForLease(ctx context.Context, l ProjectEnvironmentCloneLease, sourceID string, oid uint32) (ProjectEnvironmentClonePostgresImport, error) {
	i, _, err := s.mutateClonePostgresImport(ctx, l, sourceID, oid, "read", copyarchive.RestoreExecution{})
	return i, err
}

func (s *PgStore) ClaimProjectEnvironmentClonePostgresImport(ctx context.Context, l ProjectEnvironmentCloneLease, sourceID string, oid uint32) (ProjectEnvironmentClonePostgresImport, bool, error) {
	return s.mutateClonePostgresImport(ctx, l, sourceID, oid, "claim", copyarchive.RestoreExecution{})
}

// Trusted callers supply actual successful execution or independently recovered
// evidence for the exact owned input/target. Metadata cannot establish SQL IO.
// An unknown outcome holds its importing owner and never receives redispatch.
func (s *PgStore) RecordProjectEnvironmentClonePostgresImportExecution(ctx context.Context, l ProjectEnvironmentCloneLease, sourceID string, oid uint32, execution copyarchive.RestoreExecution) (ProjectEnvironmentClonePostgresImport, error) {
	i, _, err := s.mutateClonePostgresImport(ctx, l, sourceID, oid, "record", execution)
	return i, err
}
