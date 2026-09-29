package state

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// TriggerClaimFinisher rejects outcomes from a dispatcher whose claim lease
// was recovered and claimed again. The generation belongs to one claim, not
// to a broker delivery handle or an application work key.
type TriggerClaimFinisher interface {
	CompleteClaimedTriggerRecord(context.Context, string, int64) error
	RetryClaimedTriggerRecord(context.Context, string, int64, string, time.Time) error
	DeadLetterClaimedTriggerRecord(context.Context, string, int64, string) error
	RouteClaimedTriggerDeadLetter(context.Context, string, int64, string, string, []byte) error
}

// TriggerBatchClaimer claims only the records present in one polled broker
// batch, so a due record without its broker handle is never leased silently.
type TriggerBatchClaimer interface {
	ClaimTriggerRecordsByItems(context.Context, string, []string) ([]sqlc.TriggerRecord, error)
}

// TriggerTerminalRecordReader lets the dispatcher acknowledge a broker
// redelivery whose durable receipt was already completed or ended by a work
// policy. It never returns dead-letter rows, whose broker poison strategy may
// intentionally seek and await an operator retry.
type TriggerTerminalRecordReader interface {
	ListTerminalTriggerRecordItems(context.Context, string, []string) ([]string, error)
}

func (s *PgStore) ListTerminalTriggerRecordItems(ctx context.Context, triggerID string, items []string) ([]string, error) {
	if len(items) == 0 {
		return nil, nil
	}
	terminal, err := s.triggerQueries().ListTerminalTriggerRecordItems(ctx, s.pool,
		sqlc.ListTerminalTriggerRecordItemsParams{
			TriggerID: mustPgUUID(triggerID), ItemIdentifiers: items,
		})
	if err != nil {
		return nil, fmt.Errorf("state: list terminal trigger records: %w", err)
	}
	return terminal, nil
}

func (m *MemStore) ListTerminalTriggerRecordItems(_ context.Context, triggerID string, items []string) ([]string, error) {
	if len(items) == 0 {
		return nil, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	allowed := make(map[string]bool, len(items))
	for _, item := range items {
		allowed[item] = true
	}
	var terminal []string
	for _, record := range m.records {
		if record.TriggerID.String() != triggerID || !allowed[record.ItemIdentifier] {
			continue
		}
		switch record.State {
		case "succeeded", "superseded", "cancelled", "expired":
			terminal = append(terminal, record.ItemIdentifier)
		}
	}
	return terminal, nil
}

func (s *PgStore) ClaimTriggerRecordsByItems(ctx context.Context, triggerID string, items []string) ([]sqlc.TriggerRecord, error) {
	if len(items) == 0 {
		return nil, nil
	}
	rows, err := s.triggerQueries().ClaimTriggerRecordsByItems(ctx, s.pool,
		sqlc.ClaimTriggerRecordsByItemsParams{TriggerID: mustPgUUID(triggerID), ItemIdentifiers: items})
	if err != nil {
		return nil, fmt.Errorf("state: claim trigger batch: %w", err)
	}
	out := make([]sqlc.TriggerRecord, len(rows))
	for i, row := range rows {
		out[i] = claimTriggerRecordByItemsRowToTriggerRecord(row)
	}
	return out, nil
}

func claimTriggerRecordByItemsRowToTriggerRecord(r sqlc.ClaimTriggerRecordsByItemsRow) sqlc.TriggerRecord {
	return sqlc.TriggerRecord{
		ID: r.ID, TriggerID: r.TriggerID, ItemIdentifier: r.ItemIdentifier,
		Payload: r.Payload, Headers: r.Headers, Metadata: r.Metadata,
		State: r.State, Attempts: r.Attempts, NextFireAt: r.NextFireAt,
		ReceivedAt: r.ReceivedAt, LastError: r.LastError,
		LastDispatchedAt: r.LastDispatchedAt, ClaimGeneration: r.ClaimGeneration,
		ClaimExpiresAt: r.ClaimExpiresAt,
	}
}

func (s *PgStore) CompleteClaimedTriggerRecord(ctx context.Context, id string, generation int64) error {
	n, err := s.triggerQueries().MarkClaimedTriggerRecordSucceeded(ctx, s.pool,
		sqlc.MarkClaimedTriggerRecordSucceededParams{ID: mustPgUUID(id), ClaimGeneration: generation})
	return triggerClaimTransitionError("complete", n, err)
}

func (s *PgStore) RetryClaimedTriggerRecord(ctx context.Context, id string, generation int64, lastError string, nextFireAt time.Time) error {
	n, err := s.triggerQueries().MarkClaimedTriggerRecordRetry(ctx, s.pool,
		sqlc.MarkClaimedTriggerRecordRetryParams{ID: mustPgUUID(id), ClaimGeneration: generation,
			LastError: pgtype.Text{String: lastError, Valid: lastError != ""}, NextFireAt: pgtypeFromTime(nextFireAt)})
	return triggerClaimTransitionError("retry", n, err)
}

func (s *PgStore) DeadLetterClaimedTriggerRecord(ctx context.Context, id string, generation int64, lastError string) error {
	n, err := s.triggerQueries().MarkClaimedTriggerRecordDeadLetter(ctx, s.pool,
		sqlc.MarkClaimedTriggerRecordDeadLetterParams{ID: mustPgUUID(id), ClaimGeneration: generation,
			LastError: pgtype.Text{String: lastError, Valid: lastError != ""}})
	return triggerClaimTransitionError("dead-letter", n, err)
}

// RouteClaimedTriggerDeadLetter changes the claim and writes its DLQ receipt
// in one transaction. An older generation cannot create a false DLQ receipt.
func (s *PgStore) RouteClaimedTriggerDeadLetter(ctx context.Context, id string, generation int64, triggerID, reason string, detail []byte) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("state: trigger claim dead-letter begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	n, err := s.triggerQueries().MarkClaimedTriggerRecordDeadLetter(ctx, tx,
		sqlc.MarkClaimedTriggerRecordDeadLetterParams{ID: mustPgUUID(id), ClaimGeneration: generation,
			LastError: pgtype.Text{String: string(detail), Valid: len(detail) > 0}})
	if err := triggerClaimTransitionError("dead-letter", n, err); err != nil {
		return err
	}
	if err := s.triggerQueries().InsertTriggerDeadLetter(ctx, tx, sqlc.InsertTriggerDeadLetterParams{
		RecordID: mustPgUUID(id), TriggerID: mustPgUUID(triggerID), Reason: reason, RoutedTo: "drop",
		Column5: triggerDeadLetterDetail(detail),
	}); err != nil {
		return fmt.Errorf("state: trigger claim dead-letter receipt: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("state: trigger claim dead-letter commit: %w", err)
	}
	return nil
}

func triggerDeadLetterDetail(detail []byte) []byte {
	if len(detail) == 0 {
		return []byte("{}")
	}
	if json.Valid(detail) {
		return detail
	}
	encoded, _ := json.Marshal(string(detail))
	return encoded
}

func triggerClaimTransitionError(operation string, rows int64, err error) error {
	if err != nil {
		return fmt.Errorf("state: trigger claim %s: %w", operation, err)
	}
	if rows != 1 {
		return ErrNotFound
	}
	return nil
}

func memTriggerClaimCurrent(r sqlc.TriggerRecord, generation int64) bool {
	return r.State == "claimed" && r.ClaimGeneration == generation &&
		r.ClaimExpiresAt.Valid && r.ClaimExpiresAt.Time.After(time.Now().UTC())
}

func (m *MemStore) CompleteClaimedTriggerRecord(_ context.Context, id string, generation int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.records[id]
	if !ok || !memTriggerClaimCurrent(r, generation) {
		return ErrNotFound
	}
	r.State = "succeeded"
	r.ClaimExpiresAt = pgtype.Timestamptz{}
	r.LastDispatchedAt = pgtypeFromTime(time.Now().UTC())
	m.records[id] = r
	return nil
}

func (m *MemStore) RetryClaimedTriggerRecord(_ context.Context, id string, generation int64, lastError string, nextFireAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.records[id]
	if !ok || !memTriggerClaimCurrent(r, generation) {
		return ErrNotFound
	}
	r.State = "retry"
	r.Attempts++
	r.LastError = pgtype.Text{String: lastError, Valid: lastError != ""}
	r.LastDispatchedAt = pgtypeFromTime(time.Now().UTC())
	r.NextFireAt = pgtypeFromTime(nextFireAt)
	r.ClaimExpiresAt = pgtype.Timestamptz{}
	m.records[id] = r
	return nil
}

func (m *MemStore) DeadLetterClaimedTriggerRecord(_ context.Context, id string, generation int64, lastError string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.records[id]
	if !ok || !memTriggerClaimCurrent(r, generation) {
		return ErrNotFound
	}
	r.State = "dead_letter"
	r.Attempts++
	r.LastError = pgtype.Text{String: lastError, Valid: lastError != ""}
	r.LastDispatchedAt = pgtypeFromTime(time.Now().UTC())
	r.ClaimExpiresAt = pgtype.Timestamptz{}
	m.records[id] = r
	return nil
}

func (m *MemStore) RouteClaimedTriggerDeadLetter(_ context.Context, id string, generation int64, triggerID, reason string, detail []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.records[id]
	if !ok || !memTriggerClaimCurrent(r, generation) || r.TriggerID.String() != triggerID {
		return ErrNotFound
	}
	now := time.Now().UTC()
	r.State = "dead_letter"
	r.Attempts++
	r.LastError = pgtype.Text{String: string(detail), Valid: len(detail) > 0}
	r.LastDispatchedAt = pgtypeFromTime(now)
	r.ClaimExpiresAt = pgtype.Timestamptz{}
	m.records[id] = r
	m.triggerDeadLetters = append(m.triggerDeadLetters, sqlc.TriggerDeadLetter{
		RecordID: mustPgUUID(id), TriggerID: mustPgUUID(triggerID), Reason: reason,
		RoutedTo: "drop", Detail: triggerDeadLetterDetail(detail), CreatedAt: pgtypeFromTime(now),
	})
	return nil
}
