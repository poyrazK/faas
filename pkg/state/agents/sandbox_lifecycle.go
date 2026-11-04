package agents

// Sandbox session lifecycle (ADR-20261027090013 §1, ADR-20261027090014):
// the transitions agentd owns. A replica claims a queued session under a
// lease, as it claims runs, and the session counts against the account's
// agent concurrency from then until it ends.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/agents/sqlc"
)

// SandboxClaim is a session a replica holds.
type SandboxClaim struct {
	ID         string
	AccountID  string
	Plan       string
	RAMMB      int
	LeaseToken string
	SandboxLease
}

// SandboxLease is what a renewal reports: the fields apid may change while
// agentd holds the session.
type SandboxLease struct {
	CloseRequested bool
	IdleTimeout    time.Duration
	ExpiresAt      time.Time
}

// ClaimSandbox moves the oldest claimable queued session to starting under
// token. A session is claimable when its account has fewer live runs and
// sessions than maxLive returns for its plan.
func (s *Store) ClaimSandbox(ctx context.Context, owner, token string, ttl time.Duration, maxLive func(plan string) int) (claim SandboxClaim, found bool, err error) {
	var id string
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		candidates, err := s.q.ListClaimableSandboxSessions(ctx, tx, claimWindow)
		if err != nil {
			return fmt.Errorf("list claimable sessions: %w", err)
		}
		for _, c := range candidates {
			ok, err := s.claimable(ctx, tx, c.AccountID, maxLive)
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
			n, err := s.q.StartSandboxSession(ctx, tx, sqlc.StartSandboxSessionParams{LeaseToken: token, LeaseOwner: owner,
				TtlSeconds: ttl.Seconds(), SessionID: c.ID})
			if err != nil {
				return fmt.Errorf("start session: %w", err)
			}
			if n == 1 {
				id = c.ID
				return nil
			}
		}
		return nil
	})
	if err != nil || id == "" {
		return SandboxClaim{}, false, err
	}
	row, err := s.q.GetSandboxSessionClaim(ctx, s.pool, id)
	if err != nil {
		return SandboxClaim{}, false, fmt.Errorf("read claimed session: %w", err)
	}
	return SandboxClaim{ID: row.ID, AccountID: row.AccountID, Plan: row.Plan, RAMMB: int(row.RamMb), LeaseToken: token,
		SandboxLease: SandboxLease{CloseRequested: row.CloseRequested, IdleTimeout: time.Duration(row.IdleTimeoutSec) * time.Second,
			ExpiresAt: row.ExpiresAt.Time.UTC()}}, true, nil
}

// MarkSandboxReady records where the session's VM runs and moves it to
// ready.
func (s *Store) MarkSandboxReady(ctx context.Context, id, token, nodeID, instanceID string) error {
	n, err := s.q.MarkSandboxSessionReady(ctx, s.pool, sqlc.MarkSandboxSessionReadyParams{NodeID: nodeID, InstanceID: instanceID,
		SessionID: id, LeaseToken: token})
	return fenced(n, err, "mark session ready")
}

// RenewSandbox extends the lease and records the last call agentd has seen.
// ErrLeaseLost: the session is no longer this caller's.
func (s *Store) RenewSandbox(ctx context.Context, id, token string, ttl time.Duration, lastActivity time.Time) (SandboxLease, error) {
	row, err := s.q.RenewSandboxSessionLease(ctx, s.pool, sqlc.RenewSandboxSessionLeaseParams{TtlSeconds: ttl.Seconds(),
		LastActivityAt: pgtype.Timestamptz{Time: lastActivity, Valid: true}, SessionID: id, LeaseToken: token})
	if errors.Is(err, pgx.ErrNoRows) {
		return SandboxLease{}, ErrLeaseLost
	}
	if err != nil {
		return SandboxLease{}, fmt.Errorf("renew session lease: %w", err)
	}
	return SandboxLease{CloseRequested: row.CloseRequested, IdleTimeout: time.Duration(row.IdleTimeoutSec) * time.Second,
		ExpiresAt: row.ExpiresAt.Time.UTC()}, nil
}

// SetSandboxFrozen moves a live session between ready and frozen.
func (s *Store) SetSandboxFrozen(ctx context.Context, id, token string, frozen bool) error {
	n, err := s.q.SetSandboxSessionFrozen(ctx, s.pool, sqlc.SetSandboxSessionFrozenParams{Frozen: frozen, SessionID: id, LeaseToken: token})
	return fenced(n, err, "set session frozen")
}

// RequeueSandbox returns a starting session, which has no VM, to the queue.
func (s *Store) RequeueSandbox(ctx context.Context, id, token string) error {
	n, err := s.q.RequeueSandboxSession(ctx, s.pool, sqlc.RequeueSandboxSessionParams{SessionID: id, LeaseToken: token})
	return fenced(n, err, "requeue session")
}

// FinishSandbox ends a held session in state, one of SandboxClosed,
// SandboxExpired and SandboxFailed, with its outcome.
func (s *Store) FinishSandbox(ctx context.Context, id, token, state, code, message string) error {
	if !SandboxTerminal(state) {
		return fmt.Errorf("finish session: %q is not a terminal state", state)
	}
	n, err := s.q.FinishSandboxSession(ctx, s.pool, sqlc.FinishSandboxSessionParams{State: state, OutcomeCode: code,
		OutcomeMessage: optText(message), SessionID: id, LeaseToken: token})
	return fenced(n, err, "finish session")
}

// ExpireSandboxLeases queues expired starting sessions again, fails expired
// live ones, whose VM ended with their replica's stream, and expires queued
// sessions past their end.
func (s *Store) ExpireSandboxLeases(ctx context.Context) (requeued, failed, expired int64, err error) {
	if requeued, err = s.q.RequeueExpiredStartingSandboxSessions(ctx, s.pool); err != nil {
		return 0, 0, 0, fmt.Errorf("requeue expired sessions: %w", err)
	}
	if failed, err = s.q.FailExpiredLiveSandboxSessions(ctx, s.pool); err != nil {
		return requeued, 0, 0, fmt.Errorf("fail expired sessions: %w", err)
	}
	if expired, err = s.q.ExpireQueuedSandboxSessions(ctx, s.pool); err != nil {
		return requeued, failed, 0, fmt.Errorf("expire queued sessions: %w", err)
	}
	return requeued, failed, expired, nil
}
