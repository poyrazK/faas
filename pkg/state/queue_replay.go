package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Queue receipts and invocation rows describe one delivery. Replaying either
// surface restarts both atomically, retaining their identity and failure audit.
func retryTriggerReceiptTx(ctx context.Context, tx pgx.Tx, recordID, accountID, appID string) error {
	id, err := parsePgUUID(recordID)
	if err != nil {
		return ErrNotFound
	}
	account, app := mustPgUUID(accountID), mustPgUUID(appID)
	if accountID != "" && !account.Valid || appID != "" && !app.Valid {
		return ErrNotFound
	}
	q := sqlc.New()
	row, err := q.QueueInvocationForTriggerReceipt(ctx, tx, id)
	if err == nil {
		if account.Valid && row.AccountID != account || app.Valid && row.AppID != app {
			return ErrNotFound
		}
		if err := lockInvocationReplayLaneTx(ctx, tx, row.ID, row.AccountID, row.AppID); err != nil {
			return err
		}
		_, err = q.RetryQueueDeadLetterInvocation(ctx, tx, sqlc.RetryQueueDeadLetterInvocationParams{ID: row.ID, AccountID: row.AccountID})
		return mapErr(err)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("state: queue receipt replay lookup: %w", err)
	}
	if err := q.LockTriggerReplayLane(ctx, tx, sqlc.LockTriggerReplayLaneParams{
		ID: id, ExpectedAccountID: account, ExpectedAppID: app,
	}); err != nil {
		return fmt.Errorf("state: trigger replay lane lock: %w", err)
	}
	n, err := q.RetryExternalTriggerRecordByOperator(ctx, tx, sqlc.RetryExternalTriggerRecordByOperatorParams{ID: id, ExpectedAccountID: account, ExpectedAppID: app})
	if err != nil {
		return fmt.Errorf("state: trigger receipt replay: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func lockInvocationReplayLaneTx(ctx context.Context, tx pgx.Tx, id, account, app pgtype.UUID) error {
	if err := sqlc.New().LockInvocationReplayLane(ctx, tx, sqlc.LockInvocationReplayLaneParams{
		ID: id, AccountID: account, ExpectedAppID: app,
	}); err != nil {
		return fmt.Errorf("state: invocation replay lane lock: %w", err)
	}
	return nil
}

func prepareDeadLetterReplayTx(ctx context.Context, tx pgx.Tx, accountID, appID, eventID string, limit int) ([]pgtype.UUID, error) {
	account, err := parsePgUUID(accountID)
	if err != nil {
		return nil, ErrNotFound
	}
	app, event := mustPgUUID(appID), mustPgUUID(eventID)
	if appID != "" && !app.Valid || eventID != "" && !event.Valid {
		return nil, ErrNotFound
	}
	q := sqlc.New()
	candidates, err := q.DeadLetterReplayCandidateIDs(ctx, tx, sqlc.DeadLetterReplayCandidateIDsParams{
		AccountID: account, ExpectedAppID: app, ExpectedEventID: event, CandidateLimit: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("state: dead-letter replay candidates: %w", err)
	}
	ids := make([]pgtype.UUID, 0, len(candidates))
	for _, candidate := range candidates {
		ids = append(ids, mustPgUUID(candidate))
	}
	if len(ids) == 0 {
		return ids, nil
	}
	// Failure capture locks its execution before upserting the ledger. Replay
	// must take these locks before its ledger page, even for stale projections
	// left behind by a direct operator retry. All lanes precede all source rows.
	if err := q.LockDeadLetterReplayLanes(ctx, tx, ids); err != nil {
		return nil, fmt.Errorf("state: dead-letter replay lane locks: %w", err)
	}
	if err := q.LockKeyedDeadLetterInvocationRows(ctx, tx, ids); err != nil {
		return nil, fmt.Errorf("state: dead-letter replay invocation locks: %w", err)
	}
	if err := q.LockKeyedDeadLetterTriggerRows(ctx, tx, ids); err != nil {
		return nil, fmt.Errorf("state: dead-letter replay trigger locks: %w", err)
	}
	return ids, nil
}

func (m *MemStore) retryQueueDeadLetterLocked(accountID, invocationID string, now time.Time) (Invocation, error) {
	inv, ok := m.invocations[invocationID]
	if !ok || inv.AccountID != accountID || inv.State != InvocationDeadLetter {
		return Invocation{}, ErrNotFound
	}
	inv.State, inv.Attempts, inv.QuotaReserved = InvocationPending, 0, false
	inv.ReplayGeneration++
	inv.LastError, inv.InstanceID = "", ""
	inv.Outcome, inv.Result, inv.LeaseExpiresAt, inv.CompletedAt = nil, nil, nil, nil
	inv.DueAt, inv.LastReplayedAt = now, &now
	m.setInvocationLocked(invocationID, inv)
	if inv.Source != InvocationQueue && inv.Source != InvocationDelayedTask {
		return inv, nil
	}
	for id, r := range m.records {
		t, ok := m.triggers[r.TriggerID.String()]
		if !ok || t.Kind != "queue" || !t.Source.Valid || t.Source.String != string(inv.Source) ||
			!sameMemUUID(t.AppID.String(), inv.AppID) || !sameMemUUID(t.AccountID.String(), inv.AccountID) ||
			r.ItemIdentifier != inv.ID || inv.QueueBindingID != "" && !sameMemUUID(t.QueueBindingID.String(), inv.QueueBindingID) ||
			r.State == "superseded" || r.State == "cancelled" || r.State == "expired" {
			continue
		}
		r.State, r.Attempts, r.LastError = "pending", 0, pgtype.Text{}
		r.NextFireAt, r.ClaimExpiresAt = pgtypeFromTime(now), pgtype.Timestamptz{}
		r.ClaimGeneration++
		m.records[id] = r
	}
	return inv, nil
}

func (m *MemStore) retryTriggerReceiptLocked(id string, now time.Time) error {
	r, ok := m.records[id]
	if !ok || r.State == "superseded" || r.State == "cancelled" || r.State == "expired" {
		return ErrNotFound
	}
	t, ok := m.triggers[r.TriggerID.String()]
	if !ok {
		return ErrNotFound
	}
	if t.Kind == "queue" && t.Source.Valid && (t.Source.String == "queue" || t.Source.String == "delayed_task") {
		inv, ok := m.invocations[r.ItemIdentifier]
		if !ok || !sameMemUUID(t.AppID.String(), inv.AppID) || !sameMemUUID(t.AccountID.String(), inv.AccountID) ||
			string(inv.Source) != t.Source.String || inv.QueueBindingID != "" && !sameMemUUID(t.QueueBindingID.String(), inv.QueueBindingID) {
			return ErrNotFound
		}
		_, err := m.retryQueueDeadLetterLocked(inv.AccountID, inv.ID, now)
		return err
	}
	r.State, r.Attempts, r.LastError = "pending", 0, pgtype.Text{}
	r.NextFireAt, r.ClaimExpiresAt = pgtypeFromTime(now), pgtype.Timestamptz{}
	r.ClaimGeneration++
	m.records[id] = r
	return nil
}

// The receipt namespace is stable across replay, so a second failure replaces
// the current projection while retaining its earlier failure details.
func (m *MemStore) retainQueueDeadLetterLocked(next sqlc.TriggerDeadLetter) {
	next.FailureHistory = []byte("[]")
	t := m.triggers[next.TriggerID.String()]
	if t.Kind == "queue" && t.Source.Valid && (t.Source.String == "queue" || t.Source.String == "delayed_task") {
		for i, previous := range m.triggerDeadLetters {
			if previous.RecordID != next.RecordID || previous.TriggerID != next.TriggerID {
				continue
			}
			var history []json.RawMessage
			_ = json.Unmarshal(previous.FailureHistory, &history)
			entry, _ := json.Marshal(struct {
				Reason    string          `json:"reason"`
				RoutedTo  string          `json:"routed_to"`
				Detail    json.RawMessage `json:"detail"`
				CreatedAt time.Time       `json:"created_at"`
			}{previous.Reason, previous.RoutedTo, previous.Detail, previous.CreatedAt.Time})
			history = append(history, entry)
			next.FailureHistory, _ = json.Marshal(history)
			m.triggerDeadLetters[i] = next
			return
		}
	}
	m.triggerDeadLetters = append(m.triggerDeadLetters, next)
}
