package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) GetWorkflowRecoveryHistory(ctx context.Context, account, app string, o api.WorkflowRecoveryHistoryOptions) (api.WorkflowRecoveryHistory, error) {
	return s.getWorkflowRecoveryHistory(ctx, account, app, o, 0)
}
func (s *PgStore) getWorkflowRecoveryHistory(ctx context.Context, account, app string, o api.WorkflowRecoveryHistoryOptions, scanLimit int) (api.WorkflowRecoveryHistory, error) {
	var out api.WorkflowRecoveryHistory
	c, limit, err := recoveryHistoryWindow(account, app, o, time.Now().UTC())
	if err != nil {
		return out, err
	}
	if scanLimit > 0 {
		limit = scanLimit
	}
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, fmt.Errorf("begin recovery history: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err = q.WorkflowRecoveryAutomaticQueueApp(ctx, tx, sqlc.WorkflowRecoveryAutomaticQueueAppParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app)}); errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	} else if err != nil {
		return out, fmt.Errorf("scope recovery history: %w", err)
	}
	if !c.HistoryFence {
		c.HistorySequence, err = q.WorkflowRecoveryHistorySequence(ctx, tx, mustPgUUID(app))
		if err != nil {
			return out, fmt.Errorf("fence durable recovery history: %w", err)
		}
		c.HistoryFence = true
	}
	rows, err := q.WorkflowRecoveryHistory(ctx, tx, sqlc.WorkflowRecoveryHistoryParams{MaxSequence: c.HistorySequence, ObservedAt: pgtypeFromTime(c.ObservedAt), RetainedAfter: pgtypeFromTime(time.Now().UTC().Add(-api.WorkflowRecoveryHistoryRetention)), AppID: mustPgUUID(app), StartAt: pgtypeFromTime(c.Start), EndAt: pgtypeFromTime(c.End), Workflow: o.Workflow, RunID: canonicalMemUUID(o.RunID), PolicyVersion: o.PolicyVersion, AfterKey: c.Key, AfterAt: pgtypeFromTime(c.At), PageLimit: int32(limit + 1)})
	if err != nil {
		return out, fmt.Errorf("read recovery history: %w", err)
	}
	entries := []api.WorkflowRecoveryHistoryEntry{}
	for _, raw := range rows {
		var e api.WorkflowRecoveryHistoryEntry
		if err = json.Unmarshal(raw, &e); err != nil {
			return out, fmt.Errorf("decode recovery history entry: %w", err)
		}
		entries = append(entries, e)
	}
	out = recoveryHistoryPage(c, limit, entries)
	if err = tx.Commit(ctx); err != nil {
		return out, fmt.Errorf("finish recovery history: %w", err)
	}
	return out, nil
}
