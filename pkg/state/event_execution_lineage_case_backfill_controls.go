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

func validLineageCaseBackfillControl(account, app, sub, id, action string, req api.ControlEventExecutionLineageCaseBackfillRequest) error {
	if err := validSubscriptionControlIDs(account, app, sub); err != nil {
		return err
	}
	parsed, err := uuid.Parse(id)
	if err != nil || parsed == uuid.Nil || req.ExpectedCreatedAt.IsZero() {
		return ErrInvalidArgument
	}
	if action != "pause" && action != "resume" && action != "cancel" {
		return ErrInvalidArgument
	}
	return nil
}
func applyLineageCaseBackfillControl(job *api.EventExecutionLineageCaseBackfill, id, action string, expected time.Time) (bool, error) {
	if job.RequestID == "" || job.Status == "not_requested" {
		return false, ErrNotFound
	}
	if err := validateLineageCaseBackfill(*job); err != nil {
		return false, err
	}
	if job.RequestID != id || !job.CreatedAt.Equal(expected) {
		return false, ErrConflict
	}
	switch action {
	case "pause":
		if job.Status == "paused" {
			return false, nil
		}
		if !runnableLineageCaseBackfill(job.Status) {
			return false, ErrConflict
		}
		job.Status = "paused"
	case "resume":
		if runnableLineageCaseBackfill(job.Status) {
			return false, nil
		}
		if job.Status != "paused" {
			return false, ErrConflict
		}
		job.Status = "pending"
	case "cancel":
		switch job.Status {
		case "completed", "cancelled", "expired":
			return false, nil
		}
		if !activeLineageCaseBackfill(job.Status) {
			return false, ErrConflict
		}
		job.Status = "cancelled"
	default:
		return false, ErrInvalidArgument
	}
	now := time.Now().UTC()
	job.UpdatedAt = &now
	return true, nil
}
func (s *PgStore) ControlEventExecutionLineageCaseBackfill(ctx context.Context, account, app, sub, id, action string, req api.ControlEventExecutionLineageCaseBackfillRequest) (api.EventExecutionLineageCaseBackfill, error) {
	var out api.EventExecutionLineageCaseBackfill
	if err := validLineageCaseBackfillControl(account, app, sub, id, action, req); err != nil {
		return out, err
	}
	id = canonicalMemUUID(id)
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
	owner, err := q.LockEventExecutionLineageCoverage(ctx, tx, sqlc.LockEventExecutionLineageCoverageParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub)})
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	progress, err := decodeLineageRepairProgress(owner.LineageRepairProgress)
	if err != nil {
		return out, err
	}
	// Derive expiry after obtaining the worker's lock; a paused scan cannot outlive its deadline.
	out = lineageCaseBackfillView(app, sub, progress.CaseBackfill)
	changed, err := applyLineageCaseBackfillControl(&out, id, action, req.ExpectedCreatedAt)
	if err != nil {
		return api.EventExecutionLineageCaseBackfill{}, err
	}
	if changed {
		if err = saveLineageCaseBackfillTx(ctx, q, tx, account, app, sub, out); err != nil {
			return api.EventExecutionLineageCaseBackfill{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return api.EventExecutionLineageCaseBackfill{}, err
	}
	return out, nil
}
func (m *MemStore) ControlEventExecutionLineageCaseBackfill(ctx context.Context, account, app, sub, id, action string, req api.ControlEventExecutionLineageCaseBackfillRequest) (api.EventExecutionLineageCaseBackfill, error) {
	var out api.EventExecutionLineageCaseBackfill
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if err := validLineageCaseBackfillControl(account, app, sub, id, action, req); err != nil {
		return out, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.eventSubscriptionControlTargetLocked(account, app, sub) {
		return out, ErrNotFound
	}
	sub = canonicalMemUUID(sub)
	id = canonicalMemUUID(id)
	coverage, ok := m.eventExecutionCoverage[sub]
	if !ok {
		return out, ErrNotFound
	}
	out = lineageCaseBackfillView(app, sub, coverage.repair.CaseBackfill)
	changed, err := applyLineageCaseBackfillControl(&out, id, action, req.ExpectedCreatedAt)
	if err != nil {
		return api.EventExecutionLineageCaseBackfill{}, err
	}
	if changed {
		if _, err = json.Marshal(out); err != nil {
			return api.EventExecutionLineageCaseBackfill{}, err
		}
		stored := cloneLineageCaseBackfill(out)
		coverage.repair.CaseBackfill = &stored
		m.eventExecutionCoverage[sub] = coverage
	}
	return out, nil
}
