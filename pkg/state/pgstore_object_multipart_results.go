package state

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ObjectMultipartCompletionStore = (*PgStore)(nil)

func (s *PgStore) lockObjectMultipartResult(ctx context.Context, u ObjectMultipartUpload) (pgx.Tx, ObjectMultipartUpload, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, ObjectMultipartUpload{}, err
	}
	q := sqlc.New()
	// Lifecycle, configuration and inventory mutations lock bucket then
	// account. Taking the account first here creates a cycle with their lock.
	if _, err = q.ObjectVersionBucketOwned(ctx, tx, sqlc.ObjectVersionBucketOwnedParams{ID: mustPgUUID(u.BucketID), AccountID: mustPgUUID(u.AccountID)}); err == nil {
		_, err = q.ObjectUsageLockAccount(ctx, tx, mustPgUUID(u.AccountID))
	}
	var old ObjectMultipartUpload
	if err == nil {
		var row sqlc.ObjectStorageMultipartUpload
		row, err = q.ObjectMultipartResultLock(ctx, tx, sqlc.ObjectMultipartResultLockParams{ID: mustPgUUID(u.ID), AccountID: mustPgUUID(u.AccountID), AppID: mustPgUUID(u.AppID), BucketID: mustPgUUID(u.BucketID)})
		if err == nil {
			old, err = objectMultipartFromSQL(row)
			if err == nil && !validMultipartResultOwner(old, u) {
				err = ErrConflict
			}
		}
	}
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, old, mapErr(err)
	}
	return tx, old, nil
}

func (s *PgStore) DispatchObjectMultipartCompletion(ctx context.Context, u ObjectMultipartUpload) error {
	tx, _, err := s.lockObjectMultipartResult(ctx, u)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	n, err := sqlc.New().ObjectMultipartDispatch(ctx, tx, sqlc.ObjectMultipartDispatchParams{ID: mustPgUUID(u.ID), LeaseToken: pgtype.Text{String: u.LeaseToken, Valid: true}})
	if err != nil {
		return mapErr(err)
	}
	if n != 1 {
		return ErrConflict
	}
	return tx.Commit(ctx)
}

func (s *PgStore) FinishObjectMultipartCompletion(ctx context.Context, u ObjectMultipartUpload, result ObjectMultipartCompletionResult) (ObjectMultipartUpload, error) {
	if !validMultipartFinalResult(u, result) {
		return ObjectMultipartUpload{}, ErrConflict
	}
	tx, old, err := s.lockObjectMultipartResult(ctx, u)
	if err != nil {
		return ObjectMultipartUpload{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if !old.CompletionDispatched {
		return ObjectMultipartUpload{}, ErrConflict
	}
	var version string
	if result.ProviderVersionID != "" {
		refs, e := recordObjectVersionsTx(ctx, tx, u.BucketID, []ObjectVersionIdentity{{Key: u.Key, ProviderVersionID: result.ProviderVersionID}})
		if e != nil {
			return ObjectMultipartUpload{}, e
		}
		version = refs[0].ID
	}
	row, err := sqlc.New().ObjectMultipartFinishResult(ctx, tx, sqlc.ObjectMultipartFinishResultParams{ID: mustPgUUID(u.ID), Token: pgtype.Text{String: u.LeaseToken, Valid: true}, Etag: result.ETag, VersionID: version, VersionsObserved: result.VersionsObserved || result.ProviderVersionID != "" && result.ProviderVersionID != "null"})
	if err != nil {
		return ObjectMultipartUpload{}, mapErr(err)
	}
	out, err := objectMultipartFromSQL(row)
	if err != nil {
		return ObjectMultipartUpload{}, err
	}
	return out, tx.Commit(ctx)
}

func (s *PgStore) RetryObjectMultipartCompletion(ctx context.Context, u ObjectMultipartUpload, result ObjectMultipartCompletionResult, code string, delay time.Duration) error {
	if !validMultipartRecoveryResult(result) || !validObjectMultipartRetry(code, delay) {
		return ErrConflict
	}
	tx, _, err := s.lockObjectMultipartResult(ctx, u)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	n, err := sqlc.New().ObjectMultipartRetryResult(ctx, tx, sqlc.ObjectMultipartRetryResultParams{ID: mustPgUUID(u.ID), Token: pgtype.Text{String: u.LeaseToken, Valid: true}, Cursor: result.RecoveryCursor, VersionsObserved: result.VersionsObserved, Code: code, DelaySeconds: int32(delay / time.Second)})
	if err != nil {
		return mapErr(err)
	}
	if n != 1 {
		return ErrConflict
	}
	return tx.Commit(ctx)
}

func (s *PgStore) RejectObjectMultipartCompletionResult(ctx context.Context, u ObjectMultipartUpload, result ObjectMultipartCompletionResult, code string) error {
	if !validMultipartCompletionFailure(code) {
		return ErrConflict
	}
	tx, _, err := s.lockObjectMultipartResult(ctx, u)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	n, err := sqlc.New().ObjectMultipartRejectResult(ctx, tx, sqlc.ObjectMultipartRejectResultParams{ID: mustPgUUID(u.ID), Token: pgtype.Text{String: u.LeaseToken, Valid: true}, Code: code, VersionsObserved: result.VersionsObserved})
	if err != nil {
		return mapErr(err)
	}
	if n != 1 {
		return ErrConflict
	}
	return tx.Commit(ctx)
}
