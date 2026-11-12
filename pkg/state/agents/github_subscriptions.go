package agents

// gregale_github_subscribe's subscriptions (ADR-20261030125501 §4): a
// stateless agent's thread receives a repository's gregale.github events
// until the subscription expires. apid writes one row per tool call and reads
// the live rows of a repository when githubd forwards an event.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/agents/sqlc"
)

// GitHubSubscription is one thread's subscription to a repository's events.
type GitHubSubscription struct {
	ID         string
	AccountID  string
	EndpointID string
	ThreadID   string
	ToolCallID string
	// Repository is owner/name, lowercased.
	Repository string
	// EventTypes are gregale.github event types, such as pull_request.opened.
	EventTypes []string
	// IssuedAt is the issue time of the callback URL the call carried; a
	// thread revoked after it receives nothing.
	IssuedAt  time.Time
	ExpiresAt time.Time
}

// SubscribeGitHub records a tool call's subscription. A call already
// recorded returns its subscription with existing set, unchanged. max is the
// plan's live subscriptions per endpoint: a new subscription past it is a
// *LimitError.
func (s *Store) SubscribeGitHub(ctx context.Context, sub GitHubSubscription, now time.Time, max int) (out GitHubSubscription, existing bool, err error) {
	if !isUUID(sub.AccountID) || !isUUID(sub.EndpointID) {
		return GitHubSubscription{}, false, ErrNotFound
	}
	if sub.ThreadID == "" || sub.ToolCallID == "" || sub.Repository == "" || len(sub.EventTypes) == 0 || !sub.ExpiresAt.After(now) {
		return GitHubSubscription{}, false, fmt.Errorf("subscribe github: incomplete subscription")
	}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := s.q.LockAgentGitHubSubscriptionEndpoint(ctx, tx, sub.EndpointID); err != nil {
			return fmt.Errorf("lock endpoint subscriptions: %w", err)
		}
		row, err := s.q.AgentGitHubSubscriptionByCall(ctx, tx, sqlc.AgentGitHubSubscriptionByCallParams{EndpointID: sub.EndpointID,
			ThreadID: sub.ThreadID, ToolCallID: sub.ToolCallID})
		if err == nil {
			out, existing = sub, true
			out.ID, out.Repository, out.EventTypes, out.ExpiresAt = row.ID, row.Repository, row.EventTypes, row.ExpiresAt.Time.UTC()
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("read subscription: %w", err)
		}
		live, err := s.q.CountLiveAgentGitHubSubscriptions(ctx, tx, sqlc.CountLiveAgentGitHubSubscriptionsParams{EndpointID: sub.EndpointID,
			Now: pgtype.Timestamptz{Time: now, Valid: true}})
		if err != nil {
			return fmt.Errorf("count subscriptions: %w", err)
		}
		if int(live) >= max {
			return &LimitError{Limit: "AgentGitHubSubscriptionsPerEndpoint", Max: max, Observed: int(live)}
		}
		id, err := s.q.InsertAgentGitHubSubscription(ctx, tx, sqlc.InsertAgentGitHubSubscriptionParams{AccountID: sub.AccountID,
			EndpointID: sub.EndpointID, ThreadID: sub.ThreadID, ToolCallID: sub.ToolCallID, Repository: sub.Repository,
			EventTypes: sub.EventTypes, IssuedAt: pgtype.Timestamptz{Time: sub.IssuedAt, Valid: true},
			ExpiresAt: pgtype.Timestamptz{Time: sub.ExpiresAt, Valid: true}})
		if err != nil {
			return fmt.Errorf("insert subscription: %w", err)
		}
		out = sub
		out.ID = id
		return nil
	})
	return out, existing, err
}

// GitHubSubscribers lists up to limit live subscriptions of an account to a
// repository's event type, oldest first.
func (s *Store) GitHubSubscribers(ctx context.Context, accountID, repository, eventType string, now time.Time, limit int) ([]GitHubSubscription, error) {
	if !isUUID(accountID) {
		return nil, nil
	}
	rows, err := s.q.ListLiveAgentGitHubSubscriptions(ctx, s.pool, sqlc.ListLiveAgentGitHubSubscriptionsParams{AccountID: accountID,
		Repository: repository, EventType: eventType, Now: pgtype.Timestamptz{Time: now, Valid: true}, MaxRows: int32(limit)})
	if err != nil {
		return nil, fmt.Errorf("list github subscribers: %w", err)
	}
	out := make([]GitHubSubscription, 0, len(rows))
	for _, row := range rows {
		out = append(out, GitHubSubscription{ID: row.ID, AccountID: accountID, EndpointID: row.EndpointID, ThreadID: row.ThreadID,
			ToolCallID: row.ToolCallID, Repository: repository, IssuedAt: row.IssuedAt.Time.UTC()})
	}
	return out, nil
}

// DeleteExpiredGitHubSubscriptions deletes up to limit subscriptions that
// expired before before, and returns how many.
func (s *Store) DeleteExpiredGitHubSubscriptions(ctx context.Context, before time.Time, limit int) (int64, error) {
	n, err := s.q.DeleteExpiredAgentGitHubSubscriptions(ctx, s.pool, sqlc.DeleteExpiredAgentGitHubSubscriptionsParams{
		Before: pgtype.Timestamptz{Time: before, Valid: true}, MaxRows: int32(limit)})
	if err != nil {
		return 0, fmt.Errorf("delete expired github subscriptions: %w", err)
	}
	return n, nil
}
