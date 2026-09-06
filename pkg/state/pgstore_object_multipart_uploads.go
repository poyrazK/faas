package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ObjectMultipartUploadStore = (*PgStore)(nil)

func objectMultipartFromSQL(row sqlc.ObjectStorageMultipartUpload) (ObjectMultipartUpload, error) {
	upload := ObjectMultipartUpload{
		ID: pgUUIDString(row.ID), AccountID: pgUUIDString(row.AccountID), AppID: pgUUIDString(row.AppID), BucketID: pgUUIDString(row.BucketID),
		Key: row.ObjectKey, SizeBytes: row.SizeBytes, PartSizeBytes: row.PartSizeBytes, PartCount: row.PartCount,
		ContentType: row.ContentType, ProviderUploadID: row.ProviderUploadID, State: row.State,
		ExpiresAt: row.ExpiresAt.Time, CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
		LeaseToken: row.LeaseToken.String, LeaseUntil: row.LeaseUntil.Time, RetryAt: row.RetryAt.Time,
		AttemptCount: row.AttemptCount, LastErrorCode: row.LastErrorCode,
	}
	if err := json.Unmarshal(row.CompletionParts, &upload.Parts); err != nil {
		return ObjectMultipartUpload{}, err
	}
	if upload.Parts == nil {
		upload.Parts = []api.ObjectMultipartCompletedPart{}
	}
	return upload, nil
}

func multipartPartsJSON(parts []api.ObjectMultipartCompletedPart) ([]byte, error) {
	if parts == nil {
		parts = []api.ObjectMultipartCompletedPart{}
	}
	return json.Marshal(parts)
}

func (s *PgStore) ReserveObjectMultipartUpload(ctx context.Context, upload ObjectMultipartUpload, limit int) (ObjectMultipartUpload, error) {
	if upload.ID == "" || upload.Key == "" || upload.SizeBytes <= 0 || upload.PartSizeBytes <= 0 || upload.PartCount < 1 || upload.ExpiresAt.IsZero() || limit < 1 {
		return ObjectMultipartUpload{}, ErrConflict
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ObjectMultipartUpload{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New()
	_, err = q.ObjectMultipartLockBucket(ctx, tx, sqlc.ObjectMultipartLockBucketParams{
		ID: mustPgUUID(upload.BucketID), AccountID: mustPgUUID(upload.AccountID), AppID: mustPgUUID(upload.AppID),
	})
	if err != nil {
		return ObjectMultipartUpload{}, mapErr(err)
	}
	old, err := q.ObjectMultipartByKey(ctx, tx, sqlc.ObjectMultipartByKeyParams{
		AccountID: mustPgUUID(upload.AccountID), AppID: mustPgUUID(upload.AppID), BucketID: mustPgUUID(upload.BucketID), ObjectKey: upload.Key,
	})
	if err == nil {
		out, convertErr := objectMultipartFromSQL(old)
		if convertErr != nil {
			return ObjectMultipartUpload{}, convertErr
		}
		if out.SizeBytes != upload.SizeBytes || out.ContentType != upload.ContentType {
			return ObjectMultipartUpload{}, ErrConflict
		}
		return out, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ObjectMultipartUpload{}, err
	}
	count, err := q.ObjectMultipartCount(ctx, tx, mustPgUUID(upload.BucketID))
	if err != nil {
		return ObjectMultipartUpload{}, err
	}
	if count >= int64(limit) {
		return ObjectMultipartUpload{}, ErrConflict
	}
	row, err := q.ObjectMultipartInsert(ctx, tx, sqlc.ObjectMultipartInsertParams{
		ID: mustPgUUID(upload.ID), AccountID: mustPgUUID(upload.AccountID), AppID: mustPgUUID(upload.AppID), BucketID: mustPgUUID(upload.BucketID),
		ObjectKey: upload.Key, SizeBytes: upload.SizeBytes, PartSizeBytes: upload.PartSizeBytes, PartCount: upload.PartCount,
		ContentType: upload.ContentType, ExpiresAt: pgtype.Timestamptz{Time: upload.ExpiresAt, Valid: true},
	})
	if err != nil {
		return ObjectMultipartUpload{}, mapErr(err)
	}
	out, err := objectMultipartFromSQL(row)
	if err != nil {
		return ObjectMultipartUpload{}, err
	}
	return out, tx.Commit(ctx)
}

func (s *PgStore) GetObjectMultipartUpload(ctx context.Context, account, app, bucket, id string) (ObjectMultipartUpload, error) {
	row, err := sqlc.New().ObjectMultipartGet(ctx, s.pool, sqlc.ObjectMultipartGetParams{
		AccountID: mustPgUUID(account), AppID: mustPgUUID(app), BucketID: mustPgUUID(bucket), ID: mustPgUUID(id),
	})
	if err != nil {
		return ObjectMultipartUpload{}, mapErr(err)
	}
	return objectMultipartFromSQL(row)
}

func (s *PgStore) ListObjectMultipartUploads(ctx context.Context, account, app, bucket string, limit int32, cursor string) ([]ObjectMultipartUpload, string, error) {
	if limit < 1 || limit > 100 {
		return nil, "", ErrConflict
	}
	cursorID := pgtype.UUID{Valid: true}
	if cursor != "" {
		cursorID = mustPgUUID(cursor)
		if !cursorID.Valid {
			return nil, "", ErrConflict
		}
	}
	rows, err := sqlc.New().ObjectMultipartList(ctx, s.pool, sqlc.ObjectMultipartListParams{
		AccountID: mustPgUUID(account), AppID: mustPgUUID(app), BucketID: mustPgUUID(bucket), ID: cursorID, PageLimit: limit + 1,
	})
	if err != nil {
		return nil, "", mapErr(err)
	}
	next := ""
	if len(rows) > int(limit) {
		rows = rows[:limit]
		next = pgUUIDString(rows[len(rows)-1].ID)
	}
	out := make([]ObjectMultipartUpload, 0, len(rows))
	for _, row := range rows {
		upload, convertErr := objectMultipartFromSQL(row)
		if convertErr != nil {
			return nil, "", convertErr
		}
		out = append(out, upload)
	}
	return out, next, nil
}

func (s *PgStore) ClaimObjectMultipartUpload(ctx context.Context, account, app, bucket, id, token, operation string, parts []api.ObjectMultipartCompletedPart, recovery bool) (ObjectMultipartUpload, error) {
	if token == "" || !validObjectMultipartOperation(operation) {
		return ObjectMultipartUpload{}, ErrConflict
	}
	rawParts, err := multipartPartsJSON(parts)
	if err != nil {
		return ObjectMultipartUpload{}, ErrConflict
	}
	row, err := sqlc.New().ObjectMultipartClaim(ctx, s.pool, sqlc.ObjectMultipartClaimParams{
		Operation: operation, Token: pgtype.Text{String: token, Valid: true}, LeaseSeconds: int32(ObjectMultipartLeaseDuration / time.Second),
		CompletionParts: rawParts, AccountID: mustPgUUID(account), AppID: mustPgUUID(app), BucketID: mustPgUUID(bucket), ID: mustPgUUID(id), Recovery: recovery,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ObjectMultipartUpload{}, ErrConflict
	}
	if err != nil {
		return ObjectMultipartUpload{}, mapErr(err)
	}
	return objectMultipartFromSQL(row)
}

func (s *PgStore) ActivateObjectMultipartUpload(ctx context.Context, id, token, providerID string) error {
	if token == "" || providerID == "" || len(providerID) > 4096 {
		return ErrConflict
	}
	n, err := sqlc.New().ObjectMultipartActivate(ctx, s.pool, sqlc.ObjectMultipartActivateParams{
		ID: mustPgUUID(id), LeaseToken: pgtype.Text{String: token, Valid: true}, ProviderUploadID: providerID,
	})
	if err != nil {
		return mapErr(err)
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) FinishObjectMultipartUpload(ctx context.Context, id, token, next string) error {
	if token == "" || next != ObjectMultipartCompleted && next != ObjectMultipartAborted {
		return ErrConflict
	}
	n, err := sqlc.New().ObjectMultipartFinish(ctx, s.pool, sqlc.ObjectMultipartFinishParams{
		ID: mustPgUUID(id), LeaseToken: pgtype.Text{String: token, Valid: true}, State: next,
	})
	if err != nil {
		return mapErr(err)
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) RetryObjectMultipartUpload(ctx context.Context, id, token, code string, delay time.Duration) error {
	if token == "" || !validObjectMultipartRetry(code, delay) {
		return ErrConflict
	}
	n, err := sqlc.New().ObjectMultipartRetry(ctx, s.pool, sqlc.ObjectMultipartRetryParams{
		ID: mustPgUUID(id), LeaseToken: pgtype.Text{String: token, Valid: true}, LastErrorCode: code, Column4: int32(delay / time.Second),
	})
	if err != nil {
		return mapErr(err)
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) DueObjectMultipartUploads(ctx context.Context, limit int32) ([]ObjectMultipartUpload, error) {
	if limit < 1 || limit > 100 {
		return nil, ErrConflict
	}
	rows, err := sqlc.New().ObjectMultipartDue(ctx, s.pool, limit)
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]ObjectMultipartUpload, 0, len(rows))
	for _, row := range rows {
		upload, convertErr := objectMultipartFromSQL(row)
		if convertErr != nil {
			return nil, convertErr
		}
		out = append(out, upload)
	}
	return out, nil
}
