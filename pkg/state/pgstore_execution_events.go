package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type executionEventQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

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
	if _, err := q.Exec(ctx, `
		delete from execution_events
		 where execution_id = $1
		   and id < coalesce((
			 select id from execution_events
			  where execution_id = $1
			  order by id desc offset $2 limit 1
		   ), 0)
	`, mustPgUUID(executionID), executionEventReplayLimit-1); err != nil {
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
	rows, err := s.pool.Query(ctx, `
		select id, execution_id::text, account_id::text, event_type, payload, created_at
		  from execution_events
		 where account_id = $1 and execution_id = $2 and id > $3
		 order by id
		 limit $4
	`, mustPgUUID(accountID), mustPgUUID(executionID), afterSequence, limit)
	if err != nil {
		return nil, fmt.Errorf("state: list execution events: %w", err)
	}
	defer rows.Close()
	out := make([]ExecutionEvent, 0, limit)
	for rows.Next() {
		var event ExecutionEvent
		var createdAt time.Time
		if err := rows.Scan(&event.Sequence, &event.ExecutionID, &event.AccountID, &event.Type, &event.Payload, &createdAt); err != nil {
			return nil, fmt.Errorf("state: scan execution event: %w", err)
		}
		event.CreatedAt = createdAt.UTC()
		out = append(out, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: list execution events rows: %w", err)
	}
	return out, nil
}
