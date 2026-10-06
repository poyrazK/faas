package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copycontents"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type cloneVerificationParents struct {
	contents ProjectEnvironmentClonePostgresContents
	imported ProjectEnvironmentClonePostgresImport
	pins     ProjectEnvironmentClonePostgresDatabaseSQLPins
	target   ProjectEnvironmentClonePostgresCopyTarget
}

func cloneVerificationContextTx(ctx context.Context, tx pgx.Tx, l ProjectEnvironmentCloneLease, id string, oid uint32) (cloneVerificationParents, error) {
	var p cloneVerificationParents
	target, archive, err := clonePostgresImportContextTx(ctx, tx, l, id, oid)
	if err != nil {
		return p, err
	}
	p.target = target
	p.imported, err = readClonePostgresImportTx(ctx, tx, l, target, archive)
	if err == nil {
		p.pins, err = clonePostgresImportPreparationTx(ctx, tx, l, archive)
	}
	if err != nil {
		return p, err
	}
	parents, err := cloneContentsContextTx(ctx, tx, l, id, oid)
	if err == nil {
		p.contents, err = readCloneContentsTx(ctx, tx, parents)
	}
	if err != nil {
		return p, err
	}
	if p.contents.State != "captured" || p.imported.State == "reserved" || p.imported.ImportStartedAt.IsZero() ||
		!p.imported.MatchesDatabaseSQLPins(p.pins) || p.contents.ArchiveOwnerID != archive.OwnerID ||
		p.contents.ArchiveReservationSHA256 != archive.ReservationFingerprint() || !p.contents.Scope.Equal(archive.Scope) {
		return p, ErrConflict
	}
	return p, nil
}

func readCloneVerificationTx(ctx context.Context, tx pgx.Tx, p cloneVerificationParents) (ProjectEnvironmentClonePostgresVerification, error) {
	var zero ProjectEnvironmentClonePostgresVerification
	c, i := p.contents, p.imported
	r, err := new(sqlc.Queries).ReadProjectEnvironmentClonePostgresVerification(ctx, tx, sqlc.ReadProjectEnvironmentClonePostgresVerificationParams{
		OperationID: mustPgUUID(c.Scope.OperationID), SourceDatabaseID: mustPgUUID(c.Scope.SourceDatabaseID), DatabaseOid: int64(c.DatabaseOID)})
	if err != nil {
		return zero, mapErr(err)
	}
	scope, err := decodeCloneArchiveScope(r.Scope)
	v := ProjectEnvironmentClonePostgresVerification{Scope: scope, DatabaseOID: uint32(r.DatabaseOid), VerificationID: pgUUIDString(r.VerificationID), State: r.State,
		ContentsOwnerID: pgUUIDString(r.ContentsOwnerID), ContentsCiphertextSHA256: r.ContentsCiphertextSha256, ManifestFingerprint: r.ManifestFingerprint,
		ImportID: pgUUIDString(r.ImportID), ImportStartedAt: r.ImportStartedAt.Time, DatabaseSQLPinsCiphertextSHA256: r.DatabaseSqlPinsCiphertextSha256,
		DatabasePlanCiphertextSHA256: r.DatabasePlanCiphertextSha256, ArchiveReservationSHA256: r.ArchiveReservationSha256, TargetFingerprint: r.TargetFingerprint,
		KeyID: r.KeyID, ReservedBytes: r.ReservedBytes, CreatedAt: r.CreatedAt.Time, RequestStartedAt: r.RequestStartedAt.Time,
		ComparedAt: r.ComparedAt.Time, NativeClosedAt: r.NativeClosedAt.Time, VerifiedAt: r.VerifiedAt.Time}
	key, keyErr := age.ParseX25519Recipient(v.KeyID)
	if err != nil || keyErr != nil || key.String() != v.KeyID || !scope.Equal(c.Scope) || v.DatabaseOID != c.DatabaseOID || !validCloneCredentialSourceID(v.VerificationID) ||
		pgUUIDString(r.AccountID) != scope.AccountID || pgUUIDString(r.ProjectID) != scope.ProjectID || v.ContentsOwnerID != c.OwnerID || v.ContentsCiphertextSHA256 != c.Sealed.CiphertextSHA256 ||
		v.ManifestFingerprint != c.Sealed.Fingerprint || v.ImportID != i.ImportID || !v.ImportStartedAt.Equal(i.ImportStartedAt) || v.DatabaseSQLPinsCiphertextSHA256 != p.pins.Sealed.CiphertextSHA256 ||
		v.DatabasePlanCiphertextSHA256 != p.pins.DatabasePlanCiphertextSHA256 || v.ArchiveReservationSHA256 != p.pins.ArchiveReservationSHA256 || v.TargetFingerprint != i.TargetFingerprint ||
		v.ReservedBytes < 1 || v.ReservedBytes > api.PostgresCopyVerificationCiphertextMaxBytes || !r.CreatedAt.Valid || v.CreatedAt.Before(c.CapturedAt) || v.CreatedAt.Before(i.ImportStartedAt) || v.CreatedAt.Before(p.pins.CapturedAt) {
		return zero, ErrConflict
	}
	for _, id := range []string{scope.OperationID, scope.AccountID, scope.ProjectID, scope.SourceDatabaseID, scope.CaptureDatabaseID, c.OwnerID, i.ImportID, i.ArchiveOwnerID, i.TargetDatabaseID} {
		if v.VerificationID == id {
			return zero, ErrConflict
		}
	}
	if v.State == "compared" || v.State == "verified" {
		v.Sealed = copycontents.SealedMatch{Scope: scope, SourceDatabaseOID: v.DatabaseOID, TargetDatabaseOID: uint32(r.TargetDatabaseOid.Int64), OwnerID: v.VerificationID, ImportID: v.ImportID,
			OpenedAt: r.WindowOpenedAt.Time, ManifestFingerprint: v.ManifestFingerprint, TargetFingerprint: v.TargetFingerprint, Fingerprint: r.Fingerprint.String,
			KeyID: v.KeyID, CiphertextSHA256: r.CiphertextSha256.String, Ciphertext: bytes.Clone(r.Ciphertext)}
		if v.Sealed.ValidateMetadata() != nil || int64(len(v.Sealed.Ciphertext)) > v.ReservedBytes || !r.RequestStartedAt.Valid || !r.ComparedAt.Valid || v.ComparedAt.Before(v.RequestStartedAt) {
			return zero, ErrConflict
		}
	} else if (v.State != "reserved" && v.State != "verifying") || (v.State == "reserved") == r.RequestStartedAt.Valid || r.ComparedAt.Valid || r.WindowOpenedAt.Valid || r.TargetDatabaseOid.Valid || r.Fingerprint.Valid || r.CiphertextSha256.Valid || r.Ciphertext != nil {
		return zero, ErrConflict
	}
	if r.RequestStartedAt.Valid && v.RequestStartedAt.Before(v.CreatedAt) || v.State == "verified" && (!r.NativeClosedAt.Valid || !r.VerifiedAt.Valid || v.NativeClosedAt.Before(v.Sealed.OpenedAt) || v.VerifiedAt.Before(v.ComparedAt)) ||
		v.State != "verified" && (r.NativeClosedAt.Valid || r.VerifiedAt.Valid) {
		return zero, ErrConflict
	}
	return v, nil
}

func validCloneVerificationRequest(r ProjectEnvironmentClonePostgresVerificationRequest) bool {
	key, err := age.ParseX25519Recipient(r.KeyID)
	return r.Scope.Validate() == nil && r.DatabaseOID != 0 && validCloneObjectSHA256(r.ContentsCiphertextSHA256) && validCloneObjectSHA256(r.DatabaseSQLPinsCiphertextSHA256) &&
		validCloneCredentialSourceID(r.ImportID) && validCloneObjectSHA256(r.TargetFingerprint) && err == nil && key.String() == r.KeyID && r.ReservedBytes > 0 && r.ReservedBytes <= api.PostgresCopyVerificationCiphertextMaxBytes
}

func (s *PgStore) ReserveProjectEnvironmentClonePostgresVerification(ctx context.Context, l ProjectEnvironmentCloneLease, request ProjectEnvironmentClonePostgresVerificationRequest) (ProjectEnvironmentClonePostgresVerification, bool, error) {
	if !validCloneLeaseIdentity(l) || !validCloneVerificationRequest(request) {
		return ProjectEnvironmentClonePostgresVerification{}, false, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentClonePostgresVerification{}, false, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	p, err := cloneVerificationContextTx(ctx, tx, l, request.Scope.SourceDatabaseID, request.DatabaseOID)
	if err != nil {
		return ProjectEnvironmentClonePostgresVerification{}, false, err
	}
	c, i := p.contents, p.imported
	if l.Operation.Status != CloneOperationCapturing || p.target.State != "prepared" || !request.Scope.Equal(c.Scope) || request.ContentsCiphertextSHA256 != c.Sealed.CiphertextSHA256 ||
		request.DatabaseSQLPinsCiphertextSHA256 != p.pins.Sealed.CiphertextSHA256 || request.ImportID != i.ImportID || request.TargetFingerprint != i.TargetFingerprint {
		return ProjectEnvironmentClonePostgresVerification{}, false, ErrConflict
	}
	v, err := readCloneVerificationTx(ctx, tx, p)
	created := errors.Is(err, ErrNotFound)
	if created {
		raw, _ := json.Marshal(c.Scope)
		_, err = new(sqlc.Queries).InsertProjectEnvironmentClonePostgresVerification(ctx, tx, sqlc.InsertProjectEnvironmentClonePostgresVerificationParams{
			OperationID: mustPgUUID(c.Scope.OperationID), SourceDatabaseID: mustPgUUID(c.Scope.SourceDatabaseID), DatabaseOid: int64(c.DatabaseOID), AccountID: mustPgUUID(c.Scope.AccountID), ProjectID: mustPgUUID(c.Scope.ProjectID),
			VerificationID: mustPgUUID(uuid.NewString()), Scope: raw, ContentsOwnerID: mustPgUUID(c.OwnerID), ContentsCiphertextSha256: c.Sealed.CiphertextSHA256, ManifestFingerprint: c.Sealed.Fingerprint,
			ImportID: mustPgUUID(i.ImportID), ImportStartedAt: pgtype.Timestamptz{Time: i.ImportStartedAt, Valid: true}, DatabaseSqlPinsCiphertextSha256: p.pins.Sealed.CiphertextSHA256,
			DatabasePlanCiphertextSha256: p.pins.DatabasePlanCiphertextSHA256, ArchiveReservationSha256: p.pins.ArchiveReservationSHA256, TargetFingerprint: i.TargetFingerprint,
			KeyID: request.KeyID, ReservedBytes: request.ReservedBytes, ExpectedRevision: l.Operation.Revision, WorkerToken: l.Token})
		if err != nil {
			return v, false, cloneCopyReaderMutationError(err)
		}
		v, err = readCloneVerificationTx(ctx, tx, p)
	}
	if err != nil {
		return v, false, err
	}
	if v.KeyID != request.KeyID || v.ReservedBytes != request.ReservedBytes {
		return v, false, ErrConflict
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, l.Operation, &l); err != nil {
		return v, false, err
	}
	return v, created, mapErr(tx.Commit(ctx))
}

func (s *PgStore) mutateCloneVerification(ctx context.Context, l ProjectEnvironmentCloneLease, id string, oid uint32, action string, sealed copycontents.SealedMatch, completed ProjectEnvironmentClonePostgresVerificationCompletion) (ProjectEnvironmentClonePostgresVerification, bool, error) {
	var zero ProjectEnvironmentClonePostgresVerification
	if !validCloneLeaseIdentity(l) || !validCloneCredentialSourceID(id) || oid == 0 {
		return zero, false, ErrInvalidArgument
	}
	if action == "match" {
		if err := sealed.ValidateMetadata(); err != nil {
			if errors.Is(err, pgerrors.ErrQuotaExceeded) {
				return zero, false, ErrQuotaExceeded
			}
			return zero, false, ErrInvalidArgument
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return zero, false, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	p, err := cloneVerificationContextTx(ctx, tx, l, id, oid)
	if err != nil {
		return zero, false, err
	}
	v, err := readCloneVerificationTx(ctx, tx, p)
	if err != nil {
		return zero, false, err
	}
	if action != "read" && (l.Operation.Status != CloneOperationCapturing || p.target.State != "prepared") {
		return zero, false, ErrConflict
	}
	q, dispatch := new(sqlc.Queries), false
	if action != "read" {
		retained, e := q.HasProjectEnvironmentClonePostgresVerificationAttempts(ctx, tx, sqlc.HasProjectEnvironmentClonePostgresVerificationAttemptsParams{OperationID: mustPgUUID(v.Scope.OperationID), SourceDatabaseID: mustPgUUID(id), DatabaseOid: int64(oid)})
		if e != nil {
			return zero, false, mapErr(e)
		}
		if retained {
			return zero, false, ErrConflict
		}
	}
	switch action {
	case "read":
	case "claim":
		if v.State == "reserved" {
			_, err = q.ClaimProjectEnvironmentClonePostgresVerification(ctx, tx, sqlc.ClaimProjectEnvironmentClonePostgresVerificationParams{OperationID: mustPgUUID(v.Scope.OperationID), SourceDatabaseID: mustPgUUID(id), DatabaseOid: int64(oid), ExpectedRevision: l.Operation.Revision, WorkerToken: l.Token})
			dispatch = true
		}
	case "match":
		if v.State == "reserved" || !sealed.Scope.Equal(v.Scope) || sealed.SourceDatabaseOID != oid || sealed.OwnerID != v.VerificationID || sealed.ImportID != v.ImportID ||
			sealed.ManifestFingerprint != v.ManifestFingerprint || sealed.TargetFingerprint != v.TargetFingerprint || sealed.KeyID != v.KeyID {
			return zero, false, ErrConflict
		}
		if int64(len(sealed.Ciphertext)) > v.ReservedBytes {
			return zero, false, ErrQuotaExceeded
		}
		if v.State == "verifying" {
			_, err = q.RecordProjectEnvironmentClonePostgresVerificationMatch(ctx, tx, sqlc.RecordProjectEnvironmentClonePostgresVerificationMatchParams{OperationID: mustPgUUID(v.Scope.OperationID), SourceDatabaseID: mustPgUUID(id), DatabaseOid: int64(oid),
				WindowOpenedAt: pgtype.Timestamptz{Time: sealed.OpenedAt, Valid: true}, TargetDatabaseOid: int64(sealed.TargetDatabaseOID), Fingerprint: sealed.Fingerprint, Ciphertext: bytes.Clone(sealed.Ciphertext), CiphertextSha256: sealed.CiphertextSHA256,
				ExpectedRevision: l.Operation.Revision, WorkerToken: l.Token})
		} else if !sameCloneVerificationMatch(v.Sealed, sealed) {
			return zero, false, ErrConflict
		}
	case "close":
		target, targetErr := completed.Preparation.TargetForWorker()
		fp, fpErr := completed.Target.Fingerprint()
		owner, _ := uuid.Parse(v.VerificationID)
		imported, _ := uuid.Parse(v.ImportID)
		if (v.State != "compared" && v.State != "verified") || targetErr != nil || target != completed.Target || fpErr != nil || fp != v.TargetFingerprint ||
			completed.Manifest.Fingerprint() != v.ManifestFingerprint || !completed.Match.Matches(completed.Manifest, completed.Target, v.Sealed) ||
			completed.Closure.AttemptForWorker() != 1 || !completed.Closure.MatchesForWorker(completed.Preparation, imported, owner, v.Sealed.OpenedAt) {
			return zero, false, ErrConflict
		}
		if v.State == "compared" {
			_, err = q.RecordProjectEnvironmentClonePostgresVerificationClosure(ctx, tx, sqlc.RecordProjectEnvironmentClonePostgresVerificationClosureParams{OperationID: mustPgUUID(v.Scope.OperationID), SourceDatabaseID: mustPgUUID(id), DatabaseOid: int64(oid),
				NativeClosedAt: pgtype.Timestamptz{Time: completed.Closure.ClosedAt(), Valid: true}, ExpectedRevision: l.Operation.Revision, WorkerToken: l.Token})
		} else if !v.NativeClosedAt.Equal(completed.Closure.ClosedAt()) {
			return zero, false, ErrConflict
		}
	default:
		return zero, false, ErrInvalidArgument
	}
	if err != nil {
		return zero, false, cloneCopyReaderMutationError(err)
	}
	v, err = readCloneVerificationTx(ctx, tx, p)
	if err != nil {
		return zero, false, err
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, l.Operation, &l); err != nil {
		return zero, false, err
	}
	return v, dispatch, mapErr(tx.Commit(ctx))
}

func sameCloneVerificationMatch(a, b copycontents.SealedMatch) bool {
	return a.Scope.Equal(b.Scope) && a.SourceDatabaseOID == b.SourceDatabaseOID && a.TargetDatabaseOID == b.TargetDatabaseOID && a.OwnerID == b.OwnerID && a.ImportID == b.ImportID && a.OpenedAt.Equal(b.OpenedAt) &&
		a.ManifestFingerprint == b.ManifestFingerprint && a.TargetFingerprint == b.TargetFingerprint && a.Fingerprint == b.Fingerprint && a.KeyID == b.KeyID && a.CiphertextSHA256 == b.CiphertextSHA256 && bytes.Equal(a.Ciphertext, b.Ciphertext)
}

func (s *PgStore) ProjectEnvironmentClonePostgresVerificationForLease(ctx context.Context, l ProjectEnvironmentCloneLease, id string, oid uint32) (ProjectEnvironmentClonePostgresVerification, error) {
	v, _, err := s.mutateCloneVerification(ctx, l, id, oid, "read", copycontents.SealedMatch{}, ProjectEnvironmentClonePostgresVerificationCompletion{})
	return v, err
}
func (s *PgStore) ClaimProjectEnvironmentClonePostgresVerification(ctx context.Context, l ProjectEnvironmentCloneLease, id string, oid uint32) (ProjectEnvironmentClonePostgresVerification, bool, error) {
	return s.mutateCloneVerification(ctx, l, id, oid, "claim", copycontents.SealedMatch{}, ProjectEnvironmentClonePostgresVerificationCompletion{})
}
func (s *PgStore) RecordProjectEnvironmentClonePostgresVerificationMatch(ctx context.Context, l ProjectEnvironmentCloneLease, id string, oid uint32, sealed copycontents.SealedMatch) (ProjectEnvironmentClonePostgresVerification, error) {
	v, _, err := s.mutateCloneVerification(ctx, l, id, oid, "match", sealed, ProjectEnvironmentClonePostgresVerificationCompletion{})
	return v, err
}
func (s *PgStore) RecordProjectEnvironmentClonePostgresVerificationClosure(ctx context.Context, l ProjectEnvironmentCloneLease, id string, oid uint32, completed ProjectEnvironmentClonePostgresVerificationCompletion) (ProjectEnvironmentClonePostgresVerification, error) {
	v, _, err := s.mutateCloneVerification(ctx, l, id, oid, "close", copycontents.SealedMatch{}, completed)
	return v, err
}
