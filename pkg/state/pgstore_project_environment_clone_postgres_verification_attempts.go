package state

import (
	"bytes"
	"context"
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

func readCloneVerificationAttemptsTx(ctx context.Context, tx pgx.Tx, original ProjectEnvironmentClonePostgresVerification) ([]ProjectEnvironmentClonePostgresVerificationAttempt, error) {
	rows, err := new(sqlc.Queries).ReadProjectEnvironmentClonePostgresVerificationAttempts(ctx, tx, sqlc.ReadProjectEnvironmentClonePostgresVerificationAttemptsParams{
		OperationID: mustPgUUID(original.Scope.OperationID), SourceDatabaseID: mustPgUUID(original.Scope.SourceDatabaseID), DatabaseOid: int64(original.DatabaseOID)})
	if err != nil {
		return nil, mapErr(err)
	}
	if len(rows) > api.PostgresCopyVerificationAttemptsMax || len(rows) > 0 && original.State != "verifying" {
		return nil, ErrConflict
	}
	result := make([]ProjectEnvironmentClonePostgresVerificationAttempt, 0, len(rows))
	owners := map[string]bool{}
	for n, r := range rows {
		v := original
		v.VerificationID, v.State, v.KeyID, v.ReservedBytes = pgUUIDString(r.VerificationID), r.State, r.KeyID, r.ReservedBytes
		v.CreatedAt, v.RequestStartedAt, v.ComparedAt, v.NativeClosedAt, v.VerifiedAt = r.CreatedAt.Time, r.RequestStartedAt.Time, r.ComparedAt.Time, r.NativeClosedAt.Time, r.VerifiedAt.Time
		v.Sealed = copycontents.SealedMatch{}
		a := ProjectEnvironmentClonePostgresVerificationAttempt{ProjectEnvironmentClonePostgresVerification: v, Attempt: int32(r.Attempt), PreviousVerificationID: pgUUIDString(r.PreviousVerificationID),
			PreviousOpenedAt: r.PreviousOpenedAt.Time, PreviousClosedAt: r.PreviousClosedAt.Time, WindowOpenedAt: r.WindowOpenedAt.Time, FailedAt: r.FailedAt.Time, TargetDatabaseOID: uint32(r.TargetDatabaseOid.Int64)}
		key, keyErr := age.ParseX25519Recipient(a.KeyID)
		if int(a.Attempt) != n+1 || pgUUIDString(r.OriginalVerificationID) != original.VerificationID || !validCloneCredentialSourceID(a.VerificationID) || owners[a.VerificationID] ||
			keyErr != nil || key.String() != a.KeyID || a.ReservedBytes < 1 || a.ReservedBytes > api.PostgresCopyVerificationCiphertextMaxBytes || !r.CreatedAt.Valid ||
			a.CreatedAt.Before(original.CreatedAt) || r.RequestStartedAt.Valid && a.RequestStartedAt.Before(a.CreatedAt) {
			return nil, ErrConflict
		}
		if n == 0 {
			if a.VerificationID != original.VerificationID || a.State != "failed" || !a.CreatedAt.Equal(original.CreatedAt) || !a.RequestStartedAt.Equal(original.RequestStartedAt) ||
				a.KeyID != original.KeyID || a.ReservedBytes != original.ReservedBytes || r.PreviousAttempt.Valid || r.PreviousVerificationID.Valid || r.PreviousOpenedAt.Valid || r.PreviousClosedAt.Valid {
				return nil, ErrConflict
			}
		} else {
			p := result[n-1]
			if p.State != "failed" || !r.PreviousAttempt.Valid || int32(r.PreviousAttempt.Int16) != p.Attempt || a.PreviousVerificationID != p.VerificationID ||
				!r.PreviousOpenedAt.Valid || !r.PreviousClosedAt.Valid || !a.PreviousOpenedAt.Equal(p.WindowOpenedAt) || !a.PreviousClosedAt.Equal(p.NativeClosedAt) || a.CreatedAt.Before(p.FailedAt) ||
				r.WindowOpenedAt.Valid && (a.WindowOpenedAt.Before(p.NativeClosedAt) || a.TargetDatabaseOID != p.TargetDatabaseOID) {
				return nil, ErrConflict
			}
			for _, id := range []string{original.Scope.OperationID, original.Scope.AccountID, original.Scope.ProjectID, original.Scope.SourceDatabaseID, original.Scope.CaptureDatabaseID, original.VerificationID, original.ContentsOwnerID, original.ImportID} {
				if a.VerificationID == id {
					return nil, ErrConflict
				}
			}
		}
		switch a.State {
		case "reserved", "verifying":
			if (a.State == "verifying") != r.RequestStartedAt.Valid || r.WindowOpenedAt.Valid || r.TargetDatabaseOid.Valid {
				return nil, ErrConflict
			}
		case "failed", "compared", "verified":
			if !r.RequestStartedAt.Valid || !r.WindowOpenedAt.Valid || !r.TargetDatabaseOid.Valid || a.TargetDatabaseOID == 0 {
				return nil, ErrConflict
			}
		default:
			return nil, ErrConflict
		}
		if a.State == "compared" || a.State == "verified" {
			a.Sealed = copycontents.SealedMatch{Scope: a.Scope, SourceDatabaseOID: a.DatabaseOID, TargetDatabaseOID: a.TargetDatabaseOID, OwnerID: a.VerificationID, ImportID: a.ImportID,
				OpenedAt: a.WindowOpenedAt, ManifestFingerprint: a.ManifestFingerprint, TargetFingerprint: a.TargetFingerprint, Fingerprint: r.Fingerprint.String,
				KeyID: a.KeyID, CiphertextSHA256: r.CiphertextSha256.String, Ciphertext: bytes.Clone(r.Ciphertext)}
			if a.Sealed.ValidateMetadata() != nil || int64(len(a.Sealed.Ciphertext)) > a.ReservedBytes || !r.ComparedAt.Valid || a.ComparedAt.Before(a.RequestStartedAt) {
				return nil, ErrConflict
			}
		} else if r.Fingerprint.Valid || r.Ciphertext != nil || r.CiphertextSha256.Valid || r.ComparedAt.Valid {
			return nil, ErrConflict
		}
		if (a.State == "failed") != r.FailedAt.Valid || r.FailedAt.Valid && a.FailedAt.Before(a.RequestStartedAt) ||
			(a.State == "verified") != r.VerifiedAt.Valid || r.VerifiedAt.Valid && a.VerifiedAt.Before(a.ComparedAt) ||
			(a.State == "failed" || a.State == "verified") != r.NativeClosedAt.Valid || r.NativeClosedAt.Valid && a.NativeClosedAt.Before(a.WindowOpenedAt) {
			return nil, ErrConflict
		}
		owners[a.VerificationID] = true
		result = append(result, a)
	}
	return result, nil
}

func (s *PgStore) verificationAttemptsTx(ctx context.Context, l ProjectEnvironmentCloneLease, id string, oid uint32, mutate bool) (pgx.Tx, ProjectEnvironmentClonePostgresVerification, []ProjectEnvironmentClonePostgresVerificationAttempt, error) {
	var zero ProjectEnvironmentClonePostgresVerification
	if !validCloneLeaseIdentity(l) || !validCloneCredentialSourceID(id) || oid == 0 {
		return nil, zero, nil, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, zero, nil, mapErr(err)
	}
	fail := func(err error) (pgx.Tx, ProjectEnvironmentClonePostgresVerification, []ProjectEnvironmentClonePostgresVerificationAttempt, error) {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return nil, zero, nil, err
	}
	p, err := cloneVerificationContextTx(ctx, tx, l, id, oid)
	if err != nil {
		return fail(err)
	}
	if mutate && (l.Operation.Status != CloneOperationCapturing || p.target.State != "prepared") {
		return fail(ErrConflict)
	}
	original, err := readCloneVerificationTx(ctx, tx, p)
	if err != nil {
		return fail(err)
	}
	attempts, err := readCloneVerificationAttemptsTx(ctx, tx, original)
	if err != nil {
		return fail(err)
	}
	if err = authorizeCloneObjectMutationTx(ctx, tx, l.Operation, &l); err != nil {
		return fail(err)
	}
	return tx, original, attempts, nil
}

func finishCloneVerificationAttemptTx(ctx context.Context, tx pgx.Tx, l ProjectEnvironmentCloneLease, original ProjectEnvironmentClonePostgresVerification, owner string) (ProjectEnvironmentClonePostgresVerificationAttempt, error) {
	var zero ProjectEnvironmentClonePostgresVerificationAttempt
	attempts, err := readCloneVerificationAttemptsTx(ctx, tx, original)
	if err != nil {
		return zero, err
	}
	if err = authorizeCloneObjectMutationTx(ctx, tx, l.Operation, &l); err != nil {
		return zero, err
	}
	a := findCloneVerificationAttempt(attempts, owner)
	if a.VerificationID == "" {
		return zero, ErrConflict
	}
	if err = tx.Commit(ctx); err != nil {
		return zero, mapErr(err)
	}
	return a, nil
}
func findCloneVerificationAttempt(attempts []ProjectEnvironmentClonePostgresVerificationAttempt, owner string) ProjectEnvironmentClonePostgresVerificationAttempt {
	for _, a := range attempts {
		if a.VerificationID == owner {
			return a
		}
	}
	return ProjectEnvironmentClonePostgresVerificationAttempt{}
}
func (s *PgStore) ProjectEnvironmentClonePostgresVerificationAttemptsForLease(ctx context.Context, l ProjectEnvironmentCloneLease, id string, oid uint32) ([]ProjectEnvironmentClonePostgresVerificationAttempt, error) {
	tx, _, attempts, err := s.verificationAttemptsTx(ctx, l, id, oid, false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err = tx.Commit(ctx); err != nil {
		return nil, mapErr(err)
	}
	return attempts, nil
}

func (s *PgStore) ReserveProjectEnvironmentClonePostgresVerificationRetry(ctx context.Context, l ProjectEnvironmentCloneLease, r ProjectEnvironmentClonePostgresVerificationRetryRequest) (ProjectEnvironmentClonePostgresVerificationAttempt, bool, error) {
	var zero ProjectEnvironmentClonePostgresVerificationAttempt
	if !validCloneVerificationRequest(r.ProjectEnvironmentClonePostgresVerificationRequest) || !validCloneCredentialSourceID(r.PreviousVerificationID) {
		return zero, false, ErrInvalidArgument
	}
	tx, original, attempts, err := s.verificationAttemptsTx(ctx, l, r.Scope.SourceDatabaseID, r.DatabaseOID, true)
	if err != nil {
		return zero, false, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if !r.Scope.Equal(original.Scope) || r.ContentsCiphertextSHA256 != original.ContentsCiphertextSHA256 || r.DatabaseSQLPinsCiphertextSHA256 != original.DatabaseSQLPinsCiphertextSHA256 ||
		r.ImportID != original.ImportID || r.TargetFingerprint != original.TargetFingerprint {
		return zero, false, ErrConflict
	}
	prior := findCloneVerificationAttempt(attempts, r.PreviousVerificationID)
	if prior.State != "failed" {
		return zero, false, ErrConflict
	}
	for _, a := range attempts {
		if a.PreviousVerificationID == prior.VerificationID {
			if a.KeyID != r.KeyID || a.ReservedBytes != r.ReservedBytes {
				return zero, false, ErrConflict
			}
			got, err := finishCloneVerificationAttemptTx(ctx, tx, l, original, a.VerificationID)
			return got, false, err
		}
	}
	if prior.Attempt >= api.PostgresCopyVerificationAttemptsMax {
		return zero, false, ErrQuotaExceeded
	}
	if len(attempts) != int(prior.Attempt) {
		return zero, false, ErrConflict
	}
	owner := uuid.NewString()
	_, err = new(sqlc.Queries).InsertProjectEnvironmentClonePostgresVerificationAttempt(ctx, tx, sqlc.InsertProjectEnvironmentClonePostgresVerificationAttemptParams{
		OperationID: mustPgUUID(original.Scope.OperationID), SourceDatabaseID: mustPgUUID(original.Scope.SourceDatabaseID), DatabaseOid: int64(original.DatabaseOID), OriginalVerificationID: mustPgUUID(original.VerificationID),
		Attempt: int16(prior.Attempt + 1), VerificationID: mustPgUUID(owner), PreviousAttempt: pgtype.Int2{Int16: int16(prior.Attempt), Valid: true}, PreviousVerificationID: mustPgUUID(prior.VerificationID),
		PreviousOpenedAt: pgtype.Timestamptz{Time: prior.WindowOpenedAt, Valid: true}, PreviousClosedAt: pgtype.Timestamptz{Time: prior.NativeClosedAt, Valid: true}, KeyID: r.KeyID, ReservedBytes: r.ReservedBytes, State: "reserved", ExpectedRevision: l.Operation.Revision, WorkerToken: l.Token})
	if err != nil {
		return zero, false, cloneCopyReaderMutationError(err)
	}
	got, err := finishCloneVerificationAttemptTx(ctx, tx, l, original, owner)
	return got, err == nil, err
}

func cloneVerificationFailureMatches(v ProjectEnvironmentClonePostgresVerification, attempt int32, f ProjectEnvironmentClonePostgresVerificationFailure) bool {
	target, err := f.Preparation.TargetForWorker()
	fp, fpErr := f.Target.Fingerprint()
	owner, _ := uuid.Parse(v.VerificationID)
	imported, _ := uuid.Parse(v.ImportID)
	return err == nil && fpErr == nil && target == f.Target && fp == v.TargetFingerprint && f.Closure.AttemptForWorker() == attempt &&
		f.Closure.MatchesForWorker(f.Preparation, imported, owner, f.Closure.OpenedAt())
}

func (s *PgStore) RecordProjectEnvironmentClonePostgresVerificationFailure(ctx context.Context, l ProjectEnvironmentCloneLease, id string, oid uint32, owner string, f ProjectEnvironmentClonePostgresVerificationFailure) (ProjectEnvironmentClonePostgresVerificationAttempt, error) {
	var zero ProjectEnvironmentClonePostgresVerificationAttempt
	if !validCloneCredentialSourceID(owner) {
		return zero, ErrInvalidArgument
	}
	tx, original, attempts, err := s.verificationAttemptsTx(ctx, l, id, oid, true)
	if err != nil {
		return zero, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	a := findCloneVerificationAttempt(attempts, owner)
	if a.VerificationID == "" {
		if len(attempts) != 0 || original.VerificationID != owner || original.State != "verifying" || !cloneVerificationFailureMatches(original, 1, f) {
			return zero, ErrConflict
		}
		_, err = new(sqlc.Queries).InsertProjectEnvironmentClonePostgresVerificationAttempt(ctx, tx, sqlc.InsertProjectEnvironmentClonePostgresVerificationAttemptParams{
			OperationID: mustPgUUID(original.Scope.OperationID), SourceDatabaseID: mustPgUUID(id), DatabaseOid: int64(oid), OriginalVerificationID: mustPgUUID(owner), Attempt: 1, VerificationID: mustPgUUID(owner),
			KeyID: original.KeyID, ReservedBytes: original.ReservedBytes, State: "failed", RequestStartedAt: pgtype.Timestamptz{Time: original.RequestStartedAt, Valid: true},
			WindowOpenedAt: pgtype.Timestamptz{Time: f.Closure.OpenedAt(), Valid: true}, TargetDatabaseOid: pgtype.Int8{Int64: int64(f.Target.DatabaseOID), Valid: true}, NativeClosedAt: pgtype.Timestamptz{Time: f.Closure.ClosedAt(), Valid: true},
			CreatedAt: pgtype.Timestamptz{Time: original.CreatedAt, Valid: true}, ExpectedRevision: l.Operation.Revision, WorkerToken: l.Token})
	} else {
		if (a.State != "verifying" && a.State != "failed") || !cloneVerificationFailureMatches(a.ProjectEnvironmentClonePostgresVerification, a.Attempt, f) {
			return zero, ErrConflict
		}
		if a.State == "failed" {
			if !a.WindowOpenedAt.Equal(f.Closure.OpenedAt()) || !a.NativeClosedAt.Equal(f.Closure.ClosedAt()) || a.TargetDatabaseOID != f.Target.DatabaseOID {
				return zero, ErrConflict
			}
		} else {
			if a.Attempt == 1 || a.Attempt != int32(len(attempts)) {
				return zero, ErrConflict
			}
			_, err = new(sqlc.Queries).RecordProjectEnvironmentClonePostgresVerificationAttemptFailure(ctx, tx, sqlc.RecordProjectEnvironmentClonePostgresVerificationAttemptFailureParams{
				OperationID: mustPgUUID(original.Scope.OperationID), SourceDatabaseID: mustPgUUID(id), DatabaseOid: int64(oid), VerificationID: mustPgUUID(owner), WindowOpenedAt: pgtype.Timestamptz{Time: f.Closure.OpenedAt(), Valid: true},
				TargetDatabaseOid: int64(f.Target.DatabaseOID), NativeClosedAt: pgtype.Timestamptz{Time: f.Closure.ClosedAt(), Valid: true}, ExpectedRevision: l.Operation.Revision, WorkerToken: l.Token})
		}
	}
	if err != nil {
		return zero, cloneCopyReaderMutationError(err)
	}
	return finishCloneVerificationAttemptTx(ctx, tx, l, original, owner)
}

func (s *PgStore) mutateCloneVerificationAttempt(ctx context.Context, l ProjectEnvironmentCloneLease, id string, oid uint32, owner, action string, sealed copycontents.SealedMatch, completed ProjectEnvironmentClonePostgresVerificationCompletion) (ProjectEnvironmentClonePostgresVerificationAttempt, bool, error) {
	var zero ProjectEnvironmentClonePostgresVerificationAttempt
	if !validCloneCredentialSourceID(owner) {
		return zero, false, ErrInvalidArgument
	}
	if action == "match" && sealed.ValidateMetadata() != nil {
		if errors.Is(sealed.ValidateMetadata(), pgerrors.ErrQuotaExceeded) {
			return zero, false, ErrQuotaExceeded
		}
		return zero, false, ErrInvalidArgument
	}
	tx, original, attempts, err := s.verificationAttemptsTx(ctx, l, id, oid, true)
	if err != nil {
		return zero, false, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	a := findCloneVerificationAttempt(attempts, owner)
	if a.Attempt < 2 || a.State == "failed" || a.Attempt != int32(len(attempts)) {
		return zero, false, ErrConflict
	}
	q, dispatch := new(sqlc.Queries), false
	switch action {
	case "claim":
		if a.State == "reserved" {
			_, err = q.ClaimProjectEnvironmentClonePostgresVerificationAttempt(ctx, tx, sqlc.ClaimProjectEnvironmentClonePostgresVerificationAttemptParams{OperationID: mustPgUUID(a.Scope.OperationID), SourceDatabaseID: mustPgUUID(id), DatabaseOid: int64(oid), VerificationID: mustPgUUID(owner), ExpectedRevision: l.Operation.Revision, WorkerToken: l.Token})
			dispatch = true
		}
	case "match":
		if a.State == "reserved" || !sealed.Scope.Equal(a.Scope) || sealed.SourceDatabaseOID != oid || sealed.OwnerID != owner || sealed.ImportID != a.ImportID || sealed.ManifestFingerprint != a.ManifestFingerprint ||
			sealed.TargetFingerprint != a.TargetFingerprint || sealed.KeyID != a.KeyID || sealed.TargetDatabaseOID != attempts[len(attempts)-2].TargetDatabaseOID || sealed.OpenedAt.Before(a.PreviousClosedAt) {
			return zero, false, ErrConflict
		}
		if int64(len(sealed.Ciphertext)) > a.ReservedBytes {
			return zero, false, ErrQuotaExceeded
		}
		if a.State == "verifying" {
			_, err = q.RecordProjectEnvironmentClonePostgresVerificationAttemptMatch(ctx, tx, sqlc.RecordProjectEnvironmentClonePostgresVerificationAttemptMatchParams{OperationID: mustPgUUID(a.Scope.OperationID), SourceDatabaseID: mustPgUUID(id), DatabaseOid: int64(oid), VerificationID: mustPgUUID(owner),
				WindowOpenedAt: pgtype.Timestamptz{Time: sealed.OpenedAt, Valid: true}, TargetDatabaseOid: int64(sealed.TargetDatabaseOID), Fingerprint: sealed.Fingerprint, Ciphertext: bytes.Clone(sealed.Ciphertext), CiphertextSha256: sealed.CiphertextSHA256, ExpectedRevision: l.Operation.Revision, WorkerToken: l.Token})
		} else if !sameCloneVerificationMatch(a.Sealed, sealed) {
			return zero, false, ErrConflict
		}
	case "close":
		if (a.State != "compared" && a.State != "verified") || completed.Manifest.Fingerprint() != a.ManifestFingerprint || !completed.Match.Matches(completed.Manifest, completed.Target, a.Sealed) ||
			!cloneVerificationFailureMatches(a.ProjectEnvironmentClonePostgresVerification, a.Attempt, ProjectEnvironmentClonePostgresVerificationFailure{completed.Target, completed.Preparation, completed.Closure}) ||
			!completed.Closure.OpenedAt().Equal(a.WindowOpenedAt) {
			return zero, false, ErrConflict
		}
		if a.State == "compared" {
			_, err = q.RecordProjectEnvironmentClonePostgresVerificationAttemptClosure(ctx, tx, sqlc.RecordProjectEnvironmentClonePostgresVerificationAttemptClosureParams{OperationID: mustPgUUID(a.Scope.OperationID), SourceDatabaseID: mustPgUUID(id), DatabaseOid: int64(oid), VerificationID: mustPgUUID(owner), NativeClosedAt: pgtype.Timestamptz{Time: completed.Closure.ClosedAt(), Valid: true}, ExpectedRevision: l.Operation.Revision, WorkerToken: l.Token})
		} else if !a.NativeClosedAt.Equal(completed.Closure.ClosedAt()) {
			return zero, false, ErrConflict
		}
	default:
		return zero, false, ErrInvalidArgument
	}
	if err != nil {
		return zero, false, cloneCopyReaderMutationError(err)
	}
	got, err := finishCloneVerificationAttemptTx(ctx, tx, l, original, owner)
	return got, dispatch && err == nil, err
}
func (s *PgStore) ClaimProjectEnvironmentClonePostgresVerificationAttempt(ctx context.Context, l ProjectEnvironmentCloneLease, id string, oid uint32, owner string) (ProjectEnvironmentClonePostgresVerificationAttempt, bool, error) {
	return s.mutateCloneVerificationAttempt(ctx, l, id, oid, owner, "claim", copycontents.SealedMatch{}, ProjectEnvironmentClonePostgresVerificationCompletion{})
}
func (s *PgStore) RecordProjectEnvironmentClonePostgresVerificationAttemptMatch(ctx context.Context, l ProjectEnvironmentCloneLease, id string, oid uint32, owner string, sealed copycontents.SealedMatch) (ProjectEnvironmentClonePostgresVerificationAttempt, error) {
	a, _, err := s.mutateCloneVerificationAttempt(ctx, l, id, oid, owner, "match", sealed, ProjectEnvironmentClonePostgresVerificationCompletion{})
	return a, err
}
func (s *PgStore) RecordProjectEnvironmentClonePostgresVerificationAttemptClosure(ctx context.Context, l ProjectEnvironmentCloneLease, id string, oid uint32, owner string, completed ProjectEnvironmentClonePostgresVerificationCompletion) (ProjectEnvironmentClonePostgresVerificationAttempt, error) {
	a, _, err := s.mutateCloneVerificationAttempt(ctx, l, id, oid, owner, "close", copycontents.SealedMatch{}, completed)
	return a, err
}
