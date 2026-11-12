package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ADR-20261112160000: an invocation a function sent with send() records its
// sender, which alone may read it from its VM, and optionally the app its
// completion goes to. Both live in invocation_sends, written in the
// invocation's own transaction, so the invocation queries are unchanged.

// InvocationCompletionHeader marks a completion invocation and names the
// invocation it reports on.
const InvocationCompletionHeader = "X-Gregale-Invocation-Completion"

// InvocationSend is the send record of one invocation.
type InvocationSend struct {
	SenderAppID     string
	OnCompleteAppID string
}

// InvocationSendReader is implemented by the stores that record sends. It is
// optional so the Store interface does not grow for one gateway route.
type InvocationSendReader interface {
	InvocationSend(ctx context.Context, invocationID string) (InvocationSend, error)
}

// InvocationSend returns the send record of an invocation, or ErrNotFound
// when it was not sent with send().
func (s *PgStore) InvocationSend(ctx context.Context, invocationID string) (InvocationSend, error) {
	id, err := operationUUID(invocationID)
	if err != nil {
		return InvocationSend{}, err
	}
	row, err := sqlc.New().GetInvocationSend(ctx, s.pool, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return InvocationSend{}, ErrNotFound
	}
	if err != nil {
		return InvocationSend{}, fmt.Errorf("state: invocation send: %w", err)
	}
	return InvocationSend{SenderAppID: row.SenderAppID, OnCompleteAppID: row.OnCompleteAppID}, nil
}

// InvocationSend mirrors PgStore.InvocationSend.
func (m *MemStore) InvocationSend(_ context.Context, invocationID string) (InvocationSend, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	send, ok := m.invocationSends[invocationID]
	if !ok {
		return InvocationSend{}, ErrNotFound
	}
	return send, nil
}

// insertInvocationSendDB records inv's sender with the invocation row; db is
// the invocation's transaction.
func insertInvocationSendDB(ctx context.Context, db sqlc.DBTX, inv Invocation) error {
	if inv.SentByAppID == "" {
		return nil
	}
	params := sqlc.InsertInvocationSendParams{}
	for _, input := range []struct {
		value  string
		target *pgtype.UUID
	}{
		{inv.ID, &params.InvocationID},
		{inv.SentByAppID, &params.SenderAppID},
		{inv.OnCompleteAppID, &params.OnCompleteAppID},
	} {
		if input.value == "" {
			continue
		}
		id, err := uuid.Parse(input.value)
		if err != nil {
			return fmt.Errorf("%w: invocation send ids must be UUIDs", ErrInvalidArgument)
		}
		*input.target = pgtype.UUID{Bytes: id, Valid: true}
	}
	if err := sqlc.New().InsertInvocationSend(ctx, db, params); err != nil {
		return mapErr(err)
	}
	return nil
}

// invocationCompletion builds the completion invocation for a terminal inv
// whose send names a completion app. A completion is never sent with send()
// itself, so it schedules no completion of its own.
func invocationCompletion(inv Invocation, send InvocationSend) (Invocation, bool, error) {
	if send.OnCompleteAppID == "" {
		return Invocation{}, false, nil
	}
	outcome := OutcomeFailed
	if inv.Outcome != nil {
		outcome = *inv.Outcome
	} else if inv.State == InvocationCompleted {
		outcome = OutcomeSuccess
	}
	status := string(outcome)
	if outcome == OutcomeSuccess {
		status = "succeeded"
	}
	payload := map[string]any{
		"invocation_id": inv.ID,
		"app_id":        inv.AppID,
		"status":        status,
		"outcome":       string(outcome),
		"attempts":      inv.Attempts,
	}
	if outcome == OutcomeSuccess && len(inv.Result) > 0 {
		payload["result"] = json.RawMessage(inv.Result)
	}
	if outcome != OutcomeSuccess && inv.LastError != "" {
		payload["error"] = inv.LastError
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return Invocation{}, false, fmt.Errorf("state: build invocation completion: %w", err)
	}
	headers, err := json.Marshal(map[string]string{"Content-Type": "application/json", InvocationCompletionHeader: inv.ID})
	if err != nil {
		return Invocation{}, false, err
	}
	return Invocation{
		ID: uuid.NewString(), AppID: send.OnCompleteAppID, AccountID: inv.AccountID,
		Source: InvocationAsyncInvoke, State: InvocationPending, Method: "POST", Path: "/",
		Payload: body, Headers: headers, DueAt: time.Now().UTC(), DeploymentScope: inv.DeploymentScope,
	}, true, nil
}

// enqueueInvocationCompletionTx enqueues inv's completion invocation in its
// terminal transition's transaction.
func enqueueInvocationCompletionTx(ctx context.Context, tx pgx.Tx, inv Invocation) error {
	id, err := operationUUID(inv.ID)
	if err != nil {
		return nil
	}
	row, err := sqlc.New().GetInvocationSend(ctx, tx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("state: invocation completion send: %w", err)
	}
	completion, ok, err := invocationCompletion(inv, InvocationSend{SenderAppID: row.SenderAppID, OnCompleteAppID: row.OnCompleteAppID})
	if err != nil || !ok {
		return err
	}
	if _, err := enqueueInvocationRow(ctx, tx, completion); err != nil {
		return fmt.Errorf("state: enqueue invocation completion: %w", err)
	}
	return nil
}

// enqueueInvocationCompletionLocked mirrors enqueueInvocationCompletionTx;
// the caller holds m.mu.
func (m *MemStore) enqueueInvocationCompletionLocked(inv Invocation) error {
	completion, ok, err := invocationCompletion(inv, m.invocationSends[inv.ID])
	if err != nil || !ok {
		return err
	}
	if _, exists := m.apps[completion.AppID]; !exists {
		return nil
	}
	_, err = m.enqueueInvocationLocked(completion)
	return err
}

// recordInvocationSendLocked mirrors insertInvocationSendDB.
func (m *MemStore) recordInvocationSendLocked(inv Invocation) {
	if inv.SentByAppID == "" {
		return
	}
	if m.invocationSends == nil {
		m.invocationSends = make(map[string]InvocationSend)
	}
	m.invocationSends[inv.ID] = InvocationSend{SenderAppID: inv.SentByAppID, OnCompleteAppID: inv.OnCompleteAppID}
}

// enqueueSentInvocation writes a sent invocation and its send record in one
// transaction.
func enqueueSentInvocation(ctx context.Context, pool interface {
	Begin(context.Context) (pgx.Tx, error)
}, inv Invocation) (Invocation, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return Invocation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	out, err := enqueueInvocationRow(ctx, tx, inv)
	if err != nil {
		return Invocation{}, err
	}
	inv.ID = out.ID
	if err := insertInvocationSendDB(ctx, tx, inv); err != nil {
		return Invocation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Invocation{}, err
	}
	return out, nil
}
