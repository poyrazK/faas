package state

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func validCloneVerificationReadLimits(read, memory, disk, maxRead int64) bool {
	return read > 0 && read <= maxRead && memory >= 32 && memory <= api.PostgresCopyContentsSortMemoryMax && disk >= 32 && disk <= api.PostgresCopyContentsSortDiskMax
}

// Operation then account then children: admission serializes account totals
// without taking the account lock after another operation's child locks.
func (s *PgStore) verificationReadBudgetTx(ctx context.Context, l ProjectEnvironmentCloneLease, id string, oid uint32, mutate, account bool) (pgx.Tx, ProjectEnvironmentClonePostgresVerification, []ProjectEnvironmentClonePostgresVerificationAttempt, error) {
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
	op, err := clonePostgresSnapshotLeaseTx(ctx, tx, l)
	if err != nil {
		return fail(err)
	}
	if account {
		if _, err = new(sqlc.Queries).LockProjectEnvironmentCloneDatabaseAccount(ctx, tx, mustPgUUID(op.AccountID)); err != nil {
			return fail(mapErr(err))
		}
	}
	p, err := cloneVerificationContextTx(ctx, tx, l, id, oid)
	if err != nil {
		return fail(err)
	}
	if mutate && (op.Status != CloneOperationCapturing || p.target.State != "prepared") {
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
	if err = authorizeCloneObjectMutationTx(ctx, tx, op, &l); err != nil {
		return fail(err)
	}
	return tx, original, attempts, nil
}

func readCloneVerificationReadBudgetTx(ctx context.Context, tx pgx.Tx, original ProjectEnvironmentClonePostgresVerification, attempts []ProjectEnvironmentClonePostgresVerificationAttempt) (ProjectEnvironmentClonePostgresVerificationReadBudget, error) {
	var zero ProjectEnvironmentClonePostgresVerificationReadBudget
	q := new(sqlc.Queries)
	r, err := q.ReadProjectEnvironmentClonePostgresVerificationReadBudget(ctx, tx, sqlc.ReadProjectEnvironmentClonePostgresVerificationReadBudgetParams{
		OperationID: mustPgUUID(original.Scope.OperationID), SourceDatabaseID: mustPgUUID(original.Scope.SourceDatabaseID), DatabaseOid: int64(original.DatabaseOID)})
	if err != nil {
		return zero, mapErr(err)
	}
	b := ProjectEnvironmentClonePostgresVerificationReadBudget{Scope: original.Scope, DatabaseOID: original.DatabaseOID, OriginalVerificationID: pgUUIDString(r.OriginalVerificationID),
		ReadBytes: r.ReadBytes, SortMemoryBytes: r.SortMemoryBytes, SortDiskBytes: r.SortDiskBytes, CreatedAt: r.CreatedAt.Time}
	if b.OriginalVerificationID != original.VerificationID || pgUUIDString(r.AccountID) != b.Scope.AccountID || pgUUIDString(r.ProjectID) != b.Scope.ProjectID ||
		!validCloneVerificationReadLimits(b.ReadBytes, b.SortMemoryBytes, b.SortDiskBytes, api.PostgresCopyVerificationReadBytesPerDatabaseMax) || !r.CreatedAt.Valid || b.CreatedAt.Before(original.CreatedAt) ||
		!original.RequestStartedAt.IsZero() && b.CreatedAt.After(original.RequestStartedAt) {
		return zero, ErrConflict
	}
	rows, err := q.ReadProjectEnvironmentClonePostgresVerificationReadAllocations(ctx, tx, sqlc.ReadProjectEnvironmentClonePostgresVerificationReadAllocationsParams{
		OperationID: mustPgUUID(b.Scope.OperationID), SourceDatabaseID: mustPgUUID(b.Scope.SourceDatabaseID), DatabaseOid: int64(b.DatabaseOID)})
	if err != nil {
		return zero, mapErr(err)
	}
	if len(rows) > api.PostgresCopyVerificationAttemptsMax {
		return zero, ErrConflict
	}
	b.Allocations = make([]ProjectEnvironmentClonePostgresVerificationReadAllocation, 0, len(rows))
	for n, row := range rows {
		a := ProjectEnvironmentClonePostgresVerificationReadAllocation{Attempt: int32(row.Attempt), VerificationID: pgUUIDString(row.VerificationID), ReadBytes: row.ReadBytes,
			SortMemoryBytes: row.SortMemoryBytes, SortDiskBytes: row.SortDiskBytes, CreatedAt: row.CreatedAt.Time}
		owner := original
		if n > 0 {
			if n >= len(attempts) || !row.RetryAttempt.Valid || int32(row.RetryAttempt.Int16) != a.Attempt || pgUUIDString(row.RetryVerificationID) != a.VerificationID {
				return zero, ErrConflict
			}
			owner = attempts[n].ProjectEnvironmentClonePostgresVerification
		} else if row.RetryAttempt.Valid || row.RetryVerificationID.Valid {
			return zero, ErrConflict
		}
		if a.Attempt != int32(n+1) || a.VerificationID != owner.VerificationID || pgUUIDString(row.OriginalVerificationID) != original.VerificationID || owner.State == "reserved" || owner.RequestStartedAt.IsZero() ||
			!validCloneVerificationReadLimits(a.ReadBytes, a.SortMemoryBytes, a.SortDiskBytes, api.PostgresCopyArchiveMaxBytes) || a.SortMemoryBytes > b.SortMemoryBytes || a.SortDiskBytes > b.SortDiskBytes ||
			a.ReadBytes > b.ReadBytes-b.AllocatedReadBytes || !row.CreatedAt.Valid || a.CreatedAt.Before(b.CreatedAt) || a.CreatedAt.Before(owner.RequestStartedAt) ||
			n > 0 && a.CreatedAt.Before(b.Allocations[n-1].CreatedAt) {
			return zero, ErrConflict
		}
		b.AllocatedReadBytes += a.ReadBytes
		b.Allocations = append(b.Allocations, a)
	}
	return b, nil
}

func finishCloneVerificationReadBudgetTx(ctx context.Context, tx pgx.Tx, l ProjectEnvironmentCloneLease, original ProjectEnvironmentClonePostgresVerification, attempts []ProjectEnvironmentClonePostgresVerificationAttempt) (ProjectEnvironmentClonePostgresVerificationReadBudget, error) {
	var zero ProjectEnvironmentClonePostgresVerificationReadBudget
	b, err := readCloneVerificationReadBudgetTx(ctx, tx, original, attempts)
	if err != nil {
		return zero, err
	}
	if err = authorizeCloneObjectMutationTx(ctx, tx, l.Operation, &l); err != nil {
		return zero, err
	}
	if err = tx.Commit(ctx); err != nil {
		return zero, mapErr(err)
	}
	return b, nil
}

func (s *PgStore) ProjectEnvironmentClonePostgresVerificationReadBudgetForLease(ctx context.Context, l ProjectEnvironmentCloneLease, id string, oid uint32) (ProjectEnvironmentClonePostgresVerificationReadBudget, error) {
	tx, original, attempts, err := s.verificationReadBudgetTx(ctx, l, id, oid, false, false)
	if err != nil {
		return ProjectEnvironmentClonePostgresVerificationReadBudget{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	return finishCloneVerificationReadBudgetTx(ctx, tx, l, original, attempts)
}

func (s *PgStore) ReserveProjectEnvironmentClonePostgresVerificationReadBudget(ctx context.Context, l ProjectEnvironmentCloneLease, request ProjectEnvironmentClonePostgresVerificationReadBudgetRequest, limits ProjectEnvironmentClonePostgresVerificationReadBudgetLimits) (ProjectEnvironmentClonePostgresVerificationReadBudget, bool, error) {
	var zero ProjectEnvironmentClonePostgresVerificationReadBudget
	if request.Scope.Validate() != nil || !validCloneCredentialSourceID(request.OriginalVerificationID) ||
		!validCloneVerificationReadLimits(request.ReadBytes, request.SortMemoryBytes, request.SortDiskBytes, api.PostgresCopyVerificationReadBytesPerDatabaseMax) ||
		limits.Count < 1 || limits.Count > api.PostgresCopyContentsManifestsPerAccountMax || limits.Bytes < 1 || limits.Bytes > api.PostgresCopyVerificationReadBytesPerAccountMax {
		return zero, false, ErrInvalidArgument
	}
	tx, original, attempts, err := s.verificationReadBudgetTx(ctx, l, request.Scope.SourceDatabaseID, request.DatabaseOID, true, true)
	if err != nil {
		return zero, false, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if !request.Scope.Equal(original.Scope) || request.OriginalVerificationID != original.VerificationID {
		return zero, false, ErrConflict
	}
	b, err := readCloneVerificationReadBudgetTx(ctx, tx, original, attempts)
	created := errors.Is(err, ErrNotFound)
	if created {
		// Legacy verifying or uncertain dispatch cannot acquire retroactive credits.
		if original.State != "reserved" || len(attempts) != 0 {
			return zero, false, ErrConflict
		}
		q := new(sqlc.Queries)
		usage, err := q.CountProjectEnvironmentClonePostgresVerificationReadBudgets(ctx, tx, mustPgUUID(original.Scope.AccountID))
		if err != nil {
			return zero, false, mapErr(err)
		}
		if usage.Count >= int64(limits.Count) || request.ReadBytes > limits.Bytes || usage.Bytes > limits.Bytes-request.ReadBytes {
			return zero, false, ErrQuotaExceeded
		}
		_, err = q.InsertProjectEnvironmentClonePostgresVerificationReadBudget(ctx, tx, sqlc.InsertProjectEnvironmentClonePostgresVerificationReadBudgetParams{
			OperationID: mustPgUUID(original.Scope.OperationID), SourceDatabaseID: mustPgUUID(original.Scope.SourceDatabaseID), DatabaseOid: int64(original.DatabaseOID), OriginalVerificationID: mustPgUUID(original.VerificationID),
			AccountID: mustPgUUID(original.Scope.AccountID), ProjectID: mustPgUUID(original.Scope.ProjectID), ReadBytes: request.ReadBytes, SortMemoryBytes: request.SortMemoryBytes, SortDiskBytes: request.SortDiskBytes, ExpectedRevision: l.Operation.Revision, WorkerToken: l.Token})
		if err != nil {
			return zero, false, cloneCopyReaderMutationError(err)
		}
	} else {
		if err != nil {
			return zero, false, err
		}
		if b.ReadBytes != request.ReadBytes || b.SortMemoryBytes != request.SortMemoryBytes || b.SortDiskBytes != request.SortDiskBytes {
			return zero, false, ErrConflict
		}
	}
	b, err = finishCloneVerificationReadBudgetTx(ctx, tx, l, original, attempts)
	return b, created && err == nil, err
}

func (s *PgStore) AllocateProjectEnvironmentClonePostgresVerificationRead(ctx context.Context, l ProjectEnvironmentCloneLease, request ProjectEnvironmentClonePostgresVerificationReadRequest) (ProjectEnvironmentClonePostgresVerificationReadBudget, bool, error) {
	var zero ProjectEnvironmentClonePostgresVerificationReadBudget
	if !validCloneCredentialSourceID(request.VerificationID) || request.Attempt < 1 || request.Attempt > api.PostgresCopyVerificationAttemptsMax ||
		!validCloneVerificationReadLimits(request.ReadBytes, request.SortMemoryBytes, request.SortDiskBytes, api.PostgresCopyArchiveMaxBytes) {
		return zero, false, ErrInvalidArgument
	}
	tx, original, attempts, err := s.verificationReadBudgetTx(ctx, l, request.SourceDatabaseID, request.DatabaseOID, true, false)
	if err != nil {
		return zero, false, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	b, err := readCloneVerificationReadBudgetTx(ctx, tx, original, attempts)
	if err != nil {
		return zero, false, err
	}
	for _, a := range b.Allocations {
		if a.Attempt == request.Attempt {
			if a.VerificationID != request.VerificationID || a.ReadBytes != request.ReadBytes || a.SortMemoryBytes != request.SortMemoryBytes || a.SortDiskBytes != request.SortDiskBytes {
				return zero, false, ErrConflict
			}
			b, err = finishCloneVerificationReadBudgetTx(ctx, tx, l, original, attempts)
			return b, false, err
		}
	}
	owner := original
	if request.Attempt == 1 {
		if len(attempts) != 0 {
			return zero, false, ErrConflict
		}
	} else {
		if len(attempts) != int(request.Attempt) {
			return zero, false, ErrConflict
		}
		owner = attempts[len(attempts)-1].ProjectEnvironmentClonePostgresVerification
	}
	if owner.VerificationID != request.VerificationID || owner.State != "verifying" || int(request.Attempt) != len(b.Allocations)+1 {
		return zero, false, ErrConflict
	}
	if request.ReadBytes > b.ReadBytes-b.AllocatedReadBytes || request.SortMemoryBytes > b.SortMemoryBytes || request.SortDiskBytes > b.SortDiskBytes {
		return zero, false, ErrQuotaExceeded
	}
	retryAttempt, retryOwner := pgtype.Int2{}, pgtype.UUID{}
	if request.Attempt > 1 {
		retryAttempt = pgtype.Int2{Int16: int16(request.Attempt), Valid: true}
		retryOwner = mustPgUUID(request.VerificationID)
	}
	_, err = new(sqlc.Queries).InsertProjectEnvironmentClonePostgresVerificationReadAllocation(ctx, tx, sqlc.InsertProjectEnvironmentClonePostgresVerificationReadAllocationParams{
		OperationID: mustPgUUID(original.Scope.OperationID), SourceDatabaseID: mustPgUUID(original.Scope.SourceDatabaseID), DatabaseOid: int64(original.DatabaseOID), OriginalVerificationID: mustPgUUID(original.VerificationID), Attempt: int16(request.Attempt), VerificationID: mustPgUUID(request.VerificationID),
		RetryAttempt: retryAttempt, RetryVerificationID: retryOwner, ReadBytes: request.ReadBytes, SortMemoryBytes: request.SortMemoryBytes, SortDiskBytes: request.SortDiskBytes, ExpectedRevision: l.Operation.Revision, WorkerToken: l.Token})
	if err != nil {
		return zero, false, cloneCopyReaderMutationError(err)
	}
	b, err = finishCloneVerificationReadBudgetTx(ctx, tx, l, original, attempts)
	return b, err == nil, err
}
