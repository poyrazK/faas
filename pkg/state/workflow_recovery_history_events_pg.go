package state

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// The caller holds policy authority and commits this with its state mutation.
func appendRecoveryHistoryEvent(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, app string, e api.WorkflowRecoveryHistoryEntry) error {
	now := time.Now().UTC()
	id := canonicalMemUUID(newID())
	// PostgreSQL timestamps have microsecond precision; normalize before JSON capture.
	e.OccurredAt = e.OccurredAt.UTC().Truncate(time.Microsecond)
	e.ID = "audit:" + id
	e.Durable = true
	raw, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("encode durable recovery event: %w", err)
	}
	if err = q.WorkflowRecoveryHistoryPrune(ctx, tx, sqlc.WorkflowRecoveryHistoryPruneParams{AppID: mustPgUUID(app), BeforeAt: pgtypeFromTime(now.Add(-api.WorkflowRecoveryHistoryRetention)), KeepCount: int32(api.WorkflowRecoveryHistoryEventsMax - 1)}); err != nil {
		return fmt.Errorf("prune durable recovery events: %w", err)
	}
	if err = q.WorkflowRecoveryHistoryAppend(ctx, tx, sqlc.WorkflowRecoveryHistoryAppendParams{ID: mustPgUUID(id), AppID: mustPgUUID(app), OccurredAt: pgtypeFromTime(e.OccurredAt), RecordedAt: pgtypeFromTime(now), Entry: raw}); err != nil {
		return fmt.Errorf("append durable recovery event: %w", err)
	}
	return nil
}

func appendRecoveryRetryTransition(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, action pgtype.UUID, reason string, now time.Time, result *api.WorkflowRecoveryReviewActionStatus) error {
	row, err := q.WorkflowRecoveryHistoryAction(ctx, tx, action)
	if err != nil {
		return fmt.Errorf("read recovery event association: %w", err)
	}
	var entry api.WorkflowRecoveryHistoryEntry
	if err = json.Unmarshal(row.Entry, &entry); err != nil {
		return fmt.Errorf("decode recovery event association: %w", err)
	}
	entry.ActorKind = "system"
	entry.ActorID = "recovery_automatic_worker"
	entry.Kind = "retry_transition"
	entry.EvaluationReason = reason
	entry.OccurredAt = now
	if reason != "suppressed" {
		entry.Reason = reason
	}
	if reason == "result_recorded" && result != nil {
		entry.Outcome = result.Outcome
		entry.Reason = result.OutcomeReason
	}
	return appendRecoveryHistoryEvent(ctx, q, tx, uuidString(row.AppID), entry)
}
