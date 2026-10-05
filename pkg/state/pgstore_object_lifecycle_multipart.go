package state

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func lifecycleMultipartRows(ctx context.Context, tx pgx.Tx, j ObjectLifecycleScan) ([]ObjectMultipartUpload, error) {
	cursor := pgtype.UUID{Valid: true}
	if j.LastUploadID != "" {
		cursor = mustPgUUID(j.LastUploadID)
	}
	rows, err := sqlc.New().ObjectLifecycleMultipartList(ctx, tx, sqlc.ObjectLifecycleMultipartListParams{AccountID: mustPgUUID(j.AccountID), AppID: mustPgUUID(j.AppID), BucketID: mustPgUUID(j.BucketID), CreatedAt: objectUsageTime(j.CreatedAt), ID: cursor, PageLimit: api.ObjectLifecycleMultipartPageSize})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]ObjectMultipartUpload, 0, len(rows))
	for _, row := range rows {
		u, err := objectMultipartFromSQL(row)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, nil
}

func (s *PgStore) withLifecycleMultipartScan(ctx context.Context, id, token string, fn func(pgx.Tx, ObjectLifecycleScan, time.Time) (ObjectLifecycleScan, error)) (ObjectLifecycleScan, error) {
	initial, err := readLifecycleScan(ctx, s.pool, id)
	if err != nil {
		return initial, err
	}
	var j ObjectLifecycleScan
	_, err = s.withLifecyclePolicy(ctx, initial.AccountID, initial.AppID, initial.BucketID, func(tx pgx.Tx, p ObjectLifecyclePolicy, now time.Time) (ObjectLifecyclePolicy, error) {
		var e error
		j, e = readLifecycleScan(ctx, tx, id)
		if e != nil {
			return p, e
		}
		if p.Revision != j.Revision || j.Phase != "multipart" || !validLifecycleScanLease(j, token, now) {
			return p, ErrConflict
		}
		j, e = fn(tx, j, now)
		if e != nil {
			return p, e
		}
		if e = saveLifecycleScan(ctx, tx, j); e != nil {
			return p, e
		}
		if j.State == "completed" {
			p.NextScanAt = now.Add(api.ObjectLifecycleSweepInterval)
			return p, saveLifecyclePolicy(ctx, tx, p)
		}
		return p, nil
	})
	return j, err
}

func (s *PgStore) ListObjectLifecycleMultipartUploads(ctx context.Context, id, token string) ([]ObjectMultipartUpload, error) {
	var rows []ObjectMultipartUpload
	_, err := s.withLifecycleMultipartScan(ctx, id, token, func(tx pgx.Tx, j ObjectLifecycleScan, _ time.Time) (ObjectLifecycleScan, error) {
		var e error
		rows, e = lifecycleMultipartRows(ctx, tx, j)
		return j, e
	})
	return rows, err
}

// Account/bucket/upload locks serialize admission with completion and rule
// replacement. The abort journal and scan checkpoint commit together.
func (s *PgStore) CheckpointObjectLifecycleMultipartUpload(ctx context.Context, id, token string, expected ObjectMultipartUpload) (ObjectLifecycleScan, error) {
	return s.withLifecycleMultipartScan(ctx, id, token, func(tx pgx.Tx, j ObjectLifecycleScan, now time.Time) (ObjectLifecycleScan, error) {
		rows, err := lifecycleMultipartRows(ctx, tx, j)
		if err != nil {
			return j, err
		}
		if expected.ID == "" {
			if len(rows) != 0 {
				return j, ErrConflict
			}
			j.State, j.FinishedAt, j.Token, j.LeaseUntil, j.UpdatedAt, j.RetryAt = "completed", &now, "", time.Time{}, now, now
			return j, nil
		}
		if len(rows) != 0 && rows[0].ID < expected.ID {
			return j, ErrConflict
		}
		q := sqlc.New()
		row, err := q.ObjectMultipartResultLock(ctx, tx, sqlc.ObjectMultipartResultLockParams{ID: mustPgUUID(expected.ID), AccountID: mustPgUUID(j.AccountID), AppID: mustPgUUID(j.AppID), BucketID: mustPgUUID(j.BucketID)})
		if err != nil {
			return j, mapErr(err)
		}
		old, err := objectMultipartFromSQL(row)
		if err != nil {
			return j, err
		}
		j, next, err := checkpointLifecycleMultipart(j, token, old, expected, now)
		if err != nil {
			return j, err
		}
		if old.State != next.State {
			raw, err := json.Marshal(next.LifecycleAbort)
			if err != nil {
				return j, err
			}
			n, err := q.ObjectLifecycleMultipartAdmit(ctx, tx, sqlc.ObjectLifecycleMultipartAdmitParams{ID: row.ID, RetryAt: objectUsageTime(now), LifecycleScanID: mustPgUUID(j.ID), LifecycleBinding: raw})
			if err != nil {
				return j, mapErr(err)
			}
			if n != 1 {
				return j, ErrConflict
			}
		}
		return j, nil
	})
}
