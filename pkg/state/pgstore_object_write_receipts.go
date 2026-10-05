package state

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ObjectWriteReceiptStore = (*PgStore)(nil)

func (s *PgStore) GetObjectWriteReceipt(ctx context.Context, account, app, bucket, id string) (api.ObjectWriteReceipt, error) {
	if _, err := uuid.Parse(id); err != nil {
		return api.ObjectWriteReceipt{}, ErrNotFound
	}
	r, err := sqlc.New().ObjectWriteReceiptGet(ctx, s.pool, sqlc.ObjectWriteReceiptGetParams{ID: mustPgUUID(id), AccountID: mustPgUUID(account), AppID: mustPgUUID(app), BucketID: mustPgUUID(bucket)})
	if err != nil {
		return api.ObjectWriteReceipt{}, mapErr(err)
	}
	c, err := objectTrackedUploadFromSQL(r)
	if err != nil {
		return api.ObjectWriteReceipt{}, err
	}
	return ViewObjectWriteReceipt(c), nil
}

func (s *PgStore) ListObjectWriteReceipts(ctx context.Context, account, app, bucket, status string, limit int, cursor string) (api.ObjectWriteReceiptList, error) {
	status, limit, valid := api.ParseObjectWriteReceiptPage(status, limit, cursor)
	if !valid {
		return api.ObjectWriteReceiptList{}, ErrConflict
	}
	after, err := parseObjectWriteReceiptCursor(bucket, status, cursor)
	if err != nil {
		return api.ObjectWriteReceiptList{}, err
	}
	if _, err = s.GetObjectBucket(ctx, account, app, bucket); err != nil {
		return api.ObjectWriteReceiptList{}, err
	}
	params := sqlc.ObjectWriteReceiptsListParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), BucketID: mustPgUUID(bucket), StatusFilter: status, CursorCreated: pgtype.Timestamptz{Time: after.Created, Valid: cursor != ""}, CursorID: mustPgUUID(after.ID), PageLimit: int32(limit + 1)}
	rows, err := s.objectWriteReceiptRows(ctx, params)
	if err != nil {
		return api.ObjectWriteReceiptList{}, mapErr(err)
	}
	completions := make([]ObjectUploadCompletion, 0, len(rows))
	for _, r := range rows {
		c, e := objectTrackedUploadFromSQL(r)
		if e != nil {
			return api.ObjectWriteReceiptList{}, e
		}
		completions = append(completions, c)
	}
	return objectWriteReceiptPage(completions, bucket, status, limit), nil
}

// Separate filtered/all queries keep the status and tuple seek in index
// conditions even when PostgreSQL chooses a generic prepared-statement plan.
func (s *PgStore) objectWriteReceiptRows(ctx context.Context, p sqlc.ObjectWriteReceiptsListParams) ([]sqlc.ObjectUploadCompletion, error) {
	q := sqlc.New()
	if p.StatusFilter == "all" {
		return q.ObjectWriteReceiptsListAll(ctx, s.pool, sqlc.ObjectWriteReceiptsListAllParams{AccountID: p.AccountID, AppID: p.AppID, BucketID: p.BucketID, CursorCreated: p.CursorCreated, CursorID: p.CursorID, PageLimit: p.PageLimit})
	}
	return q.ObjectWriteReceiptsList(ctx, s.pool, p)
}
