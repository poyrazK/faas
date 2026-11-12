package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type executionEventQuerier = sqlc.DBTX

func (s *PgStore) AppendExecutionEvent(ctx context.Context, accountID, executionID string, eventType ExecutionEventType, payload json.RawMessage, at time.Time) (ExecutionEvent, error) {
	if err := validateExecutionEvent(accountID, executionID, eventType, payload, at); err != nil {
		return ExecutionEvent{}, err
	}
	return appendExecutionEvent(ctx, s.pool, accountID, executionID, eventType, payload, at)
}

func appendExecutionEvent(ctx context.Context, q executionEventQuerier, accountID, executionID string, eventType ExecutionEventType, payload json.RawMessage, at time.Time) (ExecutionEvent, error) {
	var event ExecutionEvent
	var createdAt time.Time
	err := q.QueryRow(ctx, `
		insert into execution_events (execution_id, account_id, event_type, payload, created_at)
		values ($1, $2, $3, $4::jsonb, $5)
		returning id, execution_id::text, account_id::text, event_type, payload, created_at
	`, mustPgUUID(executionID), mustPgUUID(accountID), string(eventType), []byte(payload), at.UTC()).Scan(
		&event.Sequence, &event.ExecutionID, &event.AccountID, &event.Type, &event.Payload, &createdAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ExecutionEvent{}, ErrNotFound
		}
		return ExecutionEvent{}, fmt.Errorf("state: append execution event: %w", err)
	}
	if err := sqlc.New().ExecutionPruneReplayEvents(ctx, q, sqlc.ExecutionPruneReplayEventsParams{
		ExecutionID: mustPgUUID(executionID), KeepOffset: executionEventReplayLimit - 1, PrunedAt: executionTime(at),
	}); err != nil {
		return ExecutionEvent{}, fmt.Errorf("state: prune execution events: %w", err)
	}
	event.CreatedAt = createdAt.UTC()
	return event, nil
}

func (s *PgStore) ListExecutionEvents(ctx context.Context, accountID, executionID string, afterSequence int64, limit int) ([]ExecutionEvent, error) {
	if strings.TrimSpace(accountID) == "" || strings.TrimSpace(executionID) == "" || afterSequence < 0 {
		return nil, ErrExecutionInvalid
	}
	if limit <= 0 || limit > executionEventReplayLimit {
		limit = executionEventReplayLimit
	}
	rows, err := sqlc.New().ExecutionReplayEvents(ctx, s.pool, sqlc.ExecutionReplayEventsParams{
		AccountID: mustPgUUID(accountID), ExecutionID: mustPgUUID(executionID), AfterSequence: afterSequence, PageLimit: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("state: list execution events: %w", err)
	}
	out := make([]ExecutionEvent, 0, limit)
	for _, row := range rows {
		out = append(out, ExecutionEvent{Sequence: row.ID, ExecutionID: row.ExecutionID, AccountID: row.AccountID,
			Type: ExecutionEventType(row.EventType), Payload: append(json.RawMessage(nil), row.Payload...), CreatedAt: row.CreatedAt.Time.UTC()})
	}
	return out, nil
}
