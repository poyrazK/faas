package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectrecovery"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ObjectNodeSettlementStore = (*PgStore)(nil)

func enrollObjectNodeSettlementTx(ctx context.Context, tx pgx.Tx, id, account, app, bucket, kind string, enabled bool) error {
	if !enabled {
		return nil
	}
	if !validNodeSettlementEnrollment(id, true) {
		return ErrConflict
	}
	n, err := sqlc.New().ObjectNodeSettlementEnroll(ctx, tx, sqlc.ObjectNodeSettlementEnrollParams{ID: mustPgUUID(id), AccountID: mustPgUUID(account), AppID: mustPgUUID(app), BucketID: mustPgUUID(bucket), Kind: kind})
	if err != nil {
		return mapErr(err)
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

func objectNodeSettlementFromSQL(r sqlc.ObjectNodeSettlementOutbox) (ObjectNodeSettlement, error) {
	job := ObjectNodeSettlement{ID: pgUUIDString(r.ID), AccountID: pgUUIDString(r.AccountID), AppID: pgUUIDString(r.AppID), BucketID: pgUUIDString(r.BucketID), Kind: r.Kind, BackendID: r.BackendID, BackendFingerprint: r.BackendFingerprint, PhysicalName: r.PhysicalName, CreatedAt: r.CreatedAt.Time, ReadyAt: r.ReadyAt.Time, RetryAt: r.RetryAt.Time, LeaseToken: pgUUIDString(r.LeaseToken), LeaseUntil: r.LeaseUntil.Time, Attempts: r.Attempts, LastError: r.LastError}
	if r.ReadyAt.Valid {
		decoder := json.NewDecoder(bytes.NewReader(r.Payload))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&job.Settlement) != nil || decoder.Decode(new(any)) != io.EOF {
			return job, ErrConflict
		}
		check := job
		check.ReadyAt = time.Time{}
		if _, err := readyObjectNodeSettlement(check, job.Settlement, job.ReadyAt); err != nil {
			return job, err
		}
	}
	return job, nil
}

func settleUploadNodeTx(ctx context.Context, tx pgx.Tx, c ObjectUploadCompletion, dispatched bool) error {
	return settleObjectNodeTx(ctx, tx, c.ID, c.AccountID, c.AppID, c.BucketID, "put", func(bucket string) (objectrecovery.Settlement, bool, error) {
		if !dispatched {
			return objectrecovery.Settlement{}, false, nil
		}
		return UploadNodeSettlement(bucket, c)
	})
}

func settleMultipartNodeTx(ctx context.Context, tx pgx.Tx, u ObjectMultipartUpload) error {
	return settleObjectNodeTx(ctx, tx, u.ID, u.AccountID, u.AppID, u.BucketID, "multipart", func(bucket string) (objectrecovery.Settlement, bool, error) {
		return MultipartNodeSettlement(bucket, u)
	})
}

func settleObjectNodeTx(ctx context.Context, tx pgx.Tx, id, account, app, bucket, kind string, build func(string) (objectrecovery.Settlement, bool, error)) error {
	if !validNodeSettlementEnrollment(id, true) {
		return nil // Legacy identities cannot have an enrolled job.
	}
	q := sqlc.New()
	r, err := q.ObjectNodeSettlementLock(ctx, tx, mustPgUUID(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return mapErr(err)
	}
	job, err := objectNodeSettlementFromSQL(r)
	if err != nil {
		return err
	}
	if job.AccountID != account || job.AppID != app || job.BucketID != bucket || job.Kind != kind || !job.ReadyAt.IsZero() {
		return ErrConflict
	}
	a, ready, err := build(job.PhysicalName)
	if err != nil {
		return err
	}
	var n int64
	if !ready {
		n, err = q.ObjectNodeSettlementDiscard(ctx, tx, mustPgUUID(id))
	} else {
		if _, err = readyObjectNodeSettlement(job, a, time.Now().UTC()); err != nil {
			return err
		}
		body, e := json.Marshal(a)
		if e != nil {
			return e
		}
		n, err = q.ObjectNodeSettlementReady(ctx, tx, sqlc.ObjectNodeSettlementReadyParams{ID: mustPgUUID(id), Payload: body})
	}
	if err != nil {
		return mapErr(err)
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) ClaimObjectNodeSettlements(ctx context.Context, token string, limit int32) ([]ObjectNodeSettlement, error) {
	if !validNodeSettlementClaim(token, limit) {
		return nil, ErrConflict
	}
	rows, err := sqlc.New().ObjectNodeSettlementClaim(ctx, s.pool, sqlc.ObjectNodeSettlementClaimParams{Token: mustPgUUID(token), Batch: limit, LeaseSeconds: int32(api.ObjectNodeSettlementLease / time.Second), MaxAttempts: api.ObjectNodeSettlementMaxAttempts})
	if err != nil {
		return nil, mapErr(err)
	}
	jobs := make([]ObjectNodeSettlement, 0, len(rows))
	for _, row := range rows {
		job, err := objectNodeSettlementFromSQL(row)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}

func (s *PgStore) FinishObjectNodeSettlement(ctx context.Context, id, token string) error {
	if !validNodeSettlementClaim(token, 1) || !validNodeSettlementEnrollment(id, true) {
		return ErrConflict
	}
	n, err := sqlc.New().ObjectNodeSettlementFinish(ctx, s.pool, sqlc.ObjectNodeSettlementFinishParams{ID: mustPgUUID(id), Token: mustPgUUID(token)})
	if err != nil {
		return mapErr(err)
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) RetryObjectNodeSettlement(ctx context.Context, id, token, code string, delay time.Duration) error {
	if !validNodeSettlementClaim(token, 1) || !validNodeSettlementEnrollment(id, true) || !validNodeSettlementRetry(code, delay) {
		return ErrConflict
	}
	n, err := sqlc.New().ObjectNodeSettlementRetry(ctx, s.pool, sqlc.ObjectNodeSettlementRetryParams{ID: mustPgUUID(id), Token: mustPgUUID(token), Code: code, DelaySeconds: int32(delay / time.Second)})
	if err != nil {
		return mapErr(err)
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) ObjectNodeSettlementBacklog(ctx context.Context) (ObjectNodeSettlementBacklog, error) {
	r, err := sqlc.New().ObjectNodeSettlementStats(ctx, s.pool)
	return ObjectNodeSettlementBacklog{Waiting: r.Waiting, Ready: r.Ready, OldestReadyAt: r.OldestReadyAt.Time}, mapErr(err)
}
