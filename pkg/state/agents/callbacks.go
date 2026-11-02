package agents

import (
	"context"
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
