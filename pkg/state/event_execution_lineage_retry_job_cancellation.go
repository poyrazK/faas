package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func validLineageJobCancellation(account, app, sub, id string, req api.CancelEventExecutionLineageRetryJobRequest) error {
	if err := validSubscriptionControlIDs(account, app, sub); err != nil {
		return err
	}
	u, err := uuid.Parse(id)
	if err != nil || u == uuid.Nil || req.ExpectedCreatedAt.IsZero() {
		return ErrInvalidArgument
	}
	return nil
}
func cancelLineageRetryJob(job *api.EventExecutionLineageRetryJob, expected time.Time) (bool, error) {
	if !job.CreatedAt.Equal(expected) {
		return false, ErrConflict
	}
	if job.Status == "completed" || job.Status == "cancelled" {
		return false, nil
	}
	if (job.Status != "pending" && job.Status != "paused") || job.Processed < 0 || job.Processed >= len(job.Items) || len(job.Items) > api.EventExecutionLineageRetryJobMaxCases {
		return false, ErrInvalidArgument
	}
	for _, item := range job.Items[job.Processed:] {
		if item.Status != "pending" || item.Result != nil {
			return false, ErrInvalidArgument
		}
	}
	for i := job.Processed; i < len(job.Items); i++ {
		job.Items[i].Status = "cancelled"
		job.Items[i].Reason = "job_cancelled"
	}
	job.Status = "cancelled"
	job.UpdatedAt = time.Now().UTC()
	return true, nil
}

// Controls take only the job lock, in common with the worker's first lock.
// It never takes coverage before the job, avoiding an inversion with the worker.
func (s *PgStore) controlEventExecutionLineageRetryJob(ctx context.Context, account, app, sub, id, action string, req api.ControlEventExecutionLineageRetryJobRequest) (api.EventExecutionLineageRetryJob, error) {
	var out api.EventExecutionLineageRetryJob
	if err := validLineageJobCancellation(account, app, sub, id, api.CancelEventExecutionLineageRetryJobRequest{ExpectedCreatedAt: req.ExpectedCreatedAt}); err != nil {
		return out, err
	}
	var err error
	req.CommandID, err = lineageControlCommandID(req.CommandID)
	if err != nil {
		return out, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	_, err = q.EventConsumerExecutionTarget(ctx, tx, sqlc.EventConsumerExecutionTargetParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub)})
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	row, err := q.LockEventExecutionLineageRetryJob(ctx, tx, sqlc.LockEventExecutionLineageRetryJobParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub), RequestID: mustPgUUID(id)})
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	out, err = lineageRetryJobFromRow(row)
	if err != nil {
		return out, err
	}
	// Recheck expiry after any lock wait, before changing the job.
	if !out.RetainUntil.After(time.Now()) {
		return out, ErrNotFound
	}
	if !out.CreatedAt.Equal(req.ExpectedCreatedAt) {
		return api.EventExecutionLineageRetryJob{}, ErrConflict
	}
	var sequence int64
	if req.CommandID != "" {
		prior, e := q.GetEventExecutionLineageRetryControlReceipt(ctx, tx, sqlc.GetEventExecutionLineageRetryControlReceiptParams{AccountID: row.AccountID, AppID: row.AppID, SubscriptionID: row.SubscriptionID, JobRequestID: row.RequestID, CommandID: mustPgUUID(req.CommandID)})
		if e == nil {
			receipt, e := lineageControlReceiptFromRow(prior)
			if e != nil {
				return api.EventExecutionLineageRetryJob{}, e
			}
			if e = replayLineageControlReceipt(&out, receipt, action, req.ExpectedCreatedAt); e != nil {
				return api.EventExecutionLineageRetryJob{}, e
			}
			if e = tx.Commit(ctx); e != nil {
				return api.EventExecutionLineageRetryJob{}, e
			}
			return out, nil
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return api.EventExecutionLineageRetryJob{}, e
		}
		sequence, err = q.EventExecutionLineageRetryControlSequence(ctx, tx, sqlc.EventExecutionLineageRetryControlSequenceParams{SubscriptionID: row.SubscriptionID, JobRequestID: row.RequestID})
		if err != nil {
			return api.EventExecutionLineageRetryJob{}, err
		}
	}
	var actor lineageControlActor
	if req.CommandID != "" {
		actor, err = eventExecutionLineageControlActor(ctx, account)
		if err != nil {
			return api.EventExecutionLineageRetryJob{}, err
		}
	}
	changed, err := controlLineageRetryJob(&out, action, req.ExpectedCreatedAt)
	if err != nil {
		return api.EventExecutionLineageRetryJob{}, err
	}
	if req.CommandID != "" && sequence >= api.EventExecutionLineageRetryControlMaxReceipts {
		return api.EventExecutionLineageRetryJob{}, ErrLineageControlReceiptLimit
	}
	if changed {
		raw, e := json.Marshal(out)
		if e != nil {
			return api.EventExecutionLineageRetryJob{}, e
		}
		err = q.SaveEventExecutionLineageRetryJob(ctx, tx, sqlc.SaveEventExecutionLineageRetryJobParams{SubscriptionID: row.SubscriptionID, RequestID: row.RequestID, State: out.Status, Snapshot: raw})
		if err != nil {
			return api.EventExecutionLineageRetryJob{}, err
		}
		// Return the same database timestamp as subsequent GET and idempotent cancellations.
		row, err = q.GetEventExecutionLineageRetryJob(ctx, tx, sqlc.GetEventExecutionLineageRetryJobParams{AccountID: row.AccountID, AppID: row.AppID, SubscriptionID: row.SubscriptionID, RequestID: row.RequestID})
		if errors.Is(err, pgx.ErrNoRows) {
			return api.EventExecutionLineageRetryJob{}, ErrNotFound
		}
		if err != nil {
			return api.EventExecutionLineageRetryJob{}, err
		}
		out, err = lineageRetryJobFromRow(row)
		if err != nil {
			return api.EventExecutionLineageRetryJob{}, err
		}
	}
	if req.CommandID != "" {
		at := time.Now().UTC().Truncate(time.Microsecond)
		if !out.RetainUntil.After(at) {
			return api.EventExecutionLineageRetryJob{}, ErrNotFound
		}
		receipt := newLineageControlReceipt(out, action, req.CommandID, sequence+1, at, actor)
		raw, e := json.Marshal(receipt)
		if e != nil {
			return api.EventExecutionLineageRetryJob{}, e
		}
		e = q.RecordEventExecutionLineageRetryControlReceipt(ctx, tx, sqlc.RecordEventExecutionLineageRetryControlReceiptParams{AccountID: row.AccountID, AppID: row.AppID, SubscriptionID: row.SubscriptionID, JobRequestID: row.RequestID, CommandID: mustPgUUID(req.CommandID), Action: action, ExpectedCreatedAt: row.CreatedAt, Sequence: receipt.Sequence, Result: raw, RecordedAt: pgtypeFromTime(at), RetainUntil: row.RetainUntil})
		if e != nil {
			return api.EventExecutionLineageRetryJob{}, e
		}
		out.ControlReceipt = &receipt
	}
	if err = tx.Commit(ctx); err != nil {
		return api.EventExecutionLineageRetryJob{}, err
	}
	return out, nil
}
func (m *MemStore) controlEventExecutionLineageRetryJob(ctx context.Context, account, app, sub, id, action string, req api.ControlEventExecutionLineageRetryJobRequest) (api.EventExecutionLineageRetryJob, error) {
	var out api.EventExecutionLineageRetryJob
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if err := validLineageJobCancellation(account, app, sub, id, api.CancelEventExecutionLineageRetryJobRequest{ExpectedCreatedAt: req.ExpectedCreatedAt}); err != nil {
		return out, err
	}
	var err error
	req.CommandID, err = lineageControlCommandID(req.CommandID)
	if err != nil {
		return out, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.eventSubscriptionControlTargetLocked(account, app, sub) {
		return out, ErrNotFound
	}
	sub = canonicalMemUUID(sub)
	id = canonicalMemUUID(id)
	coverage := m.eventExecutionCoverage[sub]
	job, ok := coverage.jobs[id]
	if !ok || !job.RetainUntil.After(time.Now()) {
		return out, ErrNotFound
	}
	out = cloneLineageRetryJob(job)
	if !out.CreatedAt.Equal(req.ExpectedCreatedAt) {
		return api.EventExecutionLineageRetryJob{}, ErrConflict
	}
	var sequence int64
	if req.CommandID != "" {
		if prior, ok := coverage.controlReceipts[id][req.CommandID]; ok {
			if err := replayLineageControlReceipt(&out, prior, action, req.ExpectedCreatedAt); err != nil {
				return api.EventExecutionLineageRetryJob{}, err
			}
			return out, nil
		}
		for _, receipt := range coverage.controlReceipts[id] {
			sequence = max(sequence, receipt.Sequence)
		}
	}
	var actor lineageControlActor
	if req.CommandID != "" {
		actor, err = eventExecutionLineageControlActor(ctx, account)
		if err != nil {
			return api.EventExecutionLineageRetryJob{}, err
		}
	}
	changed, err := controlLineageRetryJob(&out, action, req.ExpectedCreatedAt)
	if err != nil {
		return api.EventExecutionLineageRetryJob{}, err
	}
	if req.CommandID != "" && sequence >= api.EventExecutionLineageRetryControlMaxReceipts {
		return api.EventExecutionLineageRetryJob{}, ErrLineageControlReceiptLimit
	}
	var receipt *api.EventExecutionLineageRetryJobControlReceipt
	if req.CommandID != "" {
		at := time.Now().UTC().Truncate(time.Microsecond)
		if !out.RetainUntil.After(at) {
			return api.EventExecutionLineageRetryJob{}, ErrNotFound
		}
		value := newLineageControlReceipt(out, action, req.CommandID, sequence+1, at, actor)
		if _, err := json.Marshal(value); err != nil {
			return api.EventExecutionLineageRetryJob{}, err
		}
		receipt = &value
	}
	if changed {
		if _, err = json.Marshal(out); err != nil {
			return api.EventExecutionLineageRetryJob{}, err
		}
		coverage.jobs[id] = cloneLineageRetryJob(out)
	}
	if receipt != nil {
		if coverage.controlReceipts == nil {
			coverage.controlReceipts = make(map[string]map[string]api.EventExecutionLineageRetryJobControlReceipt)
		}
		if coverage.controlReceipts[id] == nil {
			coverage.controlReceipts[id] = make(map[string]api.EventExecutionLineageRetryJobControlReceipt)
		}
		coverage.controlReceipts[id][req.CommandID] = *receipt
		out.ControlReceipt = receipt
	}
	if changed || receipt != nil {
		m.eventExecutionCoverage[sub] = coverage
	}
	return out, nil
}
