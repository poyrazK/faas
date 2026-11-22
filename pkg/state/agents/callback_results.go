package agents

// Stored callback results (ADR-20261030125501 §3 amendment 2026-11-22): a
// stateless agent's tool result larger than one inbox message is kept in the
// artifact store, and this table records it so apid can bound an endpoint's
// live bytes, serve the result to the endpoint's app and delete it on
// expiry. The body never lives here.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/agents/sqlc"
)

// CallbackResult is one stored tool result.
type CallbackResult struct {
	ID         string
	AccountID  string
	EndpointID string
	ThreadID   string
	// MessageID is the inbox message the result belongs to; one per thread.
	MessageID string
	// SHA256 is the lowercase hex digest of the stored body.
	SHA256    string
	SizeBytes int
	ExpiresAt time.Time
}

// ReserveCallbackResult records a result before its body is stored. A
// message already recorded returns its result with existing set, unchanged,
// so a retried post finds it. maxBytes bounds the endpoint's live results:
// a new result past it is a *LimitError.
func (s *Store) ReserveCallbackResult(ctx context.Context, r CallbackResult, now time.Time, maxBytes int64) (out CallbackResult, existing bool, err error) {
	if !isUUID(r.AccountID) || !isUUID(r.EndpointID) {
		return CallbackResult{}, false, ErrNotFound
	}
	if r.ThreadID == "" || r.MessageID == "" || len(r.SHA256) != 64 || r.SizeBytes <= 0 || !r.ExpiresAt.After(now) {
		return CallbackResult{}, false, fmt.Errorf("reserve callback result: incomplete result")
	}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := s.q.LockAgentCallbackResultEndpoint(ctx, tx, r.EndpointID); err != nil {
			return fmt.Errorf("lock endpoint results: %w", err)
		}
		row, err := s.q.AgentCallbackResultByMessage(ctx, tx, sqlc.AgentCallbackResultByMessageParams{EndpointID: r.EndpointID,
			ThreadID: r.ThreadID, MessageID: r.MessageID})
		if err == nil {
			out, existing = r, true
			out.ID, out.SHA256, out.SizeBytes, out.ExpiresAt = row.ID, row.Sha256, int(row.SizeBytes), row.ExpiresAt.Time.UTC()
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("read callback result: %w", err)
		}
		live, err := s.q.LiveAgentCallbackResultBytes(ctx, tx, sqlc.LiveAgentCallbackResultBytesParams{EndpointID: r.EndpointID,
			Now: pgtype.Timestamptz{Time: now, Valid: true}})
		if err != nil {
			return fmt.Errorf("sum callback results: %w", err)
		}
		if live+int64(r.SizeBytes) > maxBytes {
			return &LimitError{Limit: "AgentCallbackResultsPerEndpointMaxBytes", Max: int(maxBytes), Observed: int(live) + r.SizeBytes}
		}
		id, err := s.q.InsertAgentCallbackResult(ctx, tx, sqlc.InsertAgentCallbackResultParams{AccountID: r.AccountID,
			EndpointID: r.EndpointID, ThreadID: r.ThreadID, MessageID: r.MessageID, Sha256: r.SHA256,
			SizeBytes: int32(r.SizeBytes), ExpiresAt: pgtype.Timestamptz{Time: r.ExpiresAt, Valid: true}})
		if err != nil {
			return fmt.Errorf("insert callback result: %w", err)
		}
		out = r
		out.ID = id
		return nil
	})
	return out, existing, err
}

// CallbackResultByID reads one of an account's stored results.
func (s *Store) CallbackResultByID(ctx context.Context, accountID, id string) (CallbackResult, error) {
	if !isUUID(accountID) || !isUUID(id) {
		return CallbackResult{}, ErrNotFound
	}
	row, err := s.q.AgentCallbackResultByID(ctx, s.pool, sqlc.AgentCallbackResultByIDParams{ID: id, AccountID: accountID})
	if errors.Is(err, pgx.ErrNoRows) {
		return CallbackResult{}, ErrNotFound
	}
	if err != nil {
		return CallbackResult{}, fmt.Errorf("read callback result: %w", err)
	}
	return CallbackResult{ID: row.ID, AccountID: accountID, EndpointID: row.EndpointID, ThreadID: row.ThreadID,
		MessageID: row.MessageID, SHA256: row.Sha256, SizeBytes: int(row.SizeBytes), ExpiresAt: row.ExpiresAt.Time.UTC()}, nil
}

// DeleteCallbackResult deletes one result's row; its body is the caller's.
func (s *Store) DeleteCallbackResult(ctx context.Context, id string) error {
	if !isUUID(id) {
		return nil
	}
	if err := s.q.DeleteAgentCallbackResult(ctx, s.pool, id); err != nil {
		return fmt.Errorf("delete callback result: %w", err)
	}
	return nil
}

// ExpiredCallbackResults lists up to limit results that expired before
// before, oldest first. The caller deletes each body, then its row.
func (s *Store) ExpiredCallbackResults(ctx context.Context, before time.Time, limit int) ([]CallbackResult, error) {
	rows, err := s.q.ExpiredAgentCallbackResults(ctx, s.pool, sqlc.ExpiredAgentCallbackResultsParams{
		Before: pgtype.Timestamptz{Time: before, Valid: true}, MaxRows: int32(limit)})
	if err != nil {
		return nil, fmt.Errorf("list expired callback results: %w", err)
	}
	out := make([]CallbackResult, 0, len(rows))
	for _, row := range rows {
		out = append(out, CallbackResult{ID: row.ID, AccountID: row.AccountID, EndpointID: row.EndpointID})
	}
	return out, nil
}
