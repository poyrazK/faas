package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/state/agents/sqlc"
)

// RevokeCallbacks revokes every callback URL of a thread issued until now
// (ADR-20261030125501 §3) and returns when. apid refuses a callback whose
// token was issued at or before that time.
func (s *Store) RevokeCallbacks(ctx context.Context, accountID, endpointID, threadID string) (time.Time, error) {
	at, err := s.q.RevokeAgentCallbacks(ctx, s.pool, sqlc.RevokeAgentCallbacksParams{EndpointID: endpointID, ThreadID: threadID, AccountID: accountID})
	if err != nil {
		return time.Time{}, fmt.Errorf("revoke callbacks: %w", err)
	}
	return at.Time, nil
}

// CallbacksRevokedAt is a thread's latest revocation; found is false when
// it was never revoked.
func (s *Store) CallbacksRevokedAt(ctx context.Context, endpointID, threadID string) (at time.Time, found bool, err error) {
	row, err := s.q.AgentCallbacksRevokedAt(ctx, s.pool, sqlc.AgentCallbacksRevokedAtParams{EndpointID: endpointID, ThreadID: threadID})
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("callback revocation: %w", err)
	}
	return row.Time, true, nil
}

// RunCallbackEnd is a finished gregale_run run whose end apid has not yet
// published into its thread (ADR-20261030125501 §4).
type RunCallbackEnd struct {
	RunID     string
	AccountID string
	Callback  RunCallback
	State     string
	// OutcomeCode and OutcomeMessage say why a run did not succeed.
	OutcomeCode    string
	OutcomeMessage string
	// Result is a succeeded run's result, if it wrote one.
	Result json.RawMessage
}

// UndeliveredRunCallbacks lists up to limit finished runs whose end is not
// yet in their thread, oldest first.
func (s *Store) UndeliveredRunCallbacks(ctx context.Context, limit int) ([]RunCallbackEnd, error) {
	rows, err := s.q.ListUndeliveredAgentRunCallbacks(ctx, s.pool, int32(limit))
	if err != nil {
		return nil, fmt.Errorf("list undelivered run callbacks: %w", err)
	}
	out := make([]RunCallbackEnd, 0, len(rows))
	for _, row := range rows {
		end := RunCallbackEnd{RunID: row.RunID, AccountID: row.AccountID, State: row.State,
			OutcomeCode: row.OutcomeCode.String, OutcomeMessage: row.OutcomeMessage.String,
			Callback: RunCallback{EndpointID: row.EndpointID, ThreadID: row.ThreadID, ToolCallID: row.ToolCallID,
				ProviderCallID: row.ProviderCallID.String, IssuedAt: row.IssuedAt.Time.UTC()}}
		if row.State == StateSucceeded && len(row.Result) > 0 {
			end.Result = json.RawMessage(row.Result)
		}
		out = append(out, end)
	}
	return out, nil
}

// MarkRunCallbackDelivered records that a run's end reached its thread, or
// that it never will (the thread was revoked or its endpoint deleted).
func (s *Store) MarkRunCallbackDelivered(ctx context.Context, runID string) error {
	if err := s.q.MarkAgentRunCallbackDelivered(ctx, s.pool, runID); err != nil {
		return fmt.Errorf("mark run callback delivered: %w", err)
	}
	return nil
}

// CallbackClock is the time on the clock RevokeCallbacks records. apid
// stamps a callback token's issue time with it, so a token compares with a
// revocation on one clock whichever apid replica issued it.
func (s *Store) CallbackClock(ctx context.Context) (time.Time, error) {
	now, err := s.q.AgentCallbackClock(ctx, s.pool)
	if err != nil {
		return time.Time{}, fmt.Errorf("callback clock: %w", err)
	}
	return now.Time, nil
}
