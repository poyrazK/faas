package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type EventRoutingRetryPolicyStore interface {
	GetEventSubscriptionRetryPolicy(context.Context, string, string, string) (api.EventRoutingRetryPolicyResponse, error)
	// Optional expected policy makes compensation conditional; nil means legacy default.
	SetEventSubscriptionRetryPolicy(context.Context, string, string, string, *api.EventRoutingRetryPolicy, ...*api.EventRoutingRetryPolicy) (*api.EventRoutingRetryPolicy, error)
}

func decodeEventRoutingRetryPolicy(b []byte) *api.EventRoutingRetryPolicy {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	var p api.EventRoutingRetryPolicy
	if json.Unmarshal(b, &p) != nil {
		return nil
	}
	return &p
}
func cloneEventRoutingRetryPolicy(p *api.EventRoutingRetryPolicy) *api.EventRoutingRetryPolicy {
	if p == nil {
		return nil
	}
	copy := *p
	return &copy
}
func eventRoutingRetryPolicyResponse(sub string, p *api.EventRoutingRetryPolicy) api.EventRoutingRetryPolicyResponse {
	out := api.EventRoutingRetryPolicyResponse{SubscriptionID: canonicalMemUUID(sub), Policy: api.DefaultEventRoutingRetryPolicy(), Configured: p != nil}
	if p != nil {
		out.Policy = *p
	}
	return out
}
func sameEventRoutingRetryPolicy(a, b *api.EventRoutingRetryPolicy) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
func (s *PgStore) GetEventSubscriptionRetryPolicy(ctx context.Context, account, app, sub string) (api.EventRoutingRetryPolicyResponse, error) {
	if err := validSubscriptionControlIDs(account, app, sub); err != nil {
		return api.EventRoutingRetryPolicyResponse{}, err
	}
	row, err := sqlc.New().EventRoutingRetryTarget(ctx, s.pool, sqlc.EventRoutingRetryTargetParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub)})
	if errors.Is(err, pgx.ErrNoRows) {
		return api.EventRoutingRetryPolicyResponse{}, ErrNotFound
	}
	if err != nil {
		return api.EventRoutingRetryPolicyResponse{}, err
	}
	return eventRoutingRetryPolicyResponse(sub, decodeEventRoutingRetryPolicy(row.RoutingRetryPolicy)), nil
}
func (s *PgStore) SetEventSubscriptionRetryPolicy(ctx context.Context, account, app, sub string, p *api.EventRoutingRetryPolicy, expected ...*api.EventRoutingRetryPolicy) (*api.EventRoutingRetryPolicy, error) {
	if err := validSubscriptionControlIDs(account, app, sub); err != nil {
		return nil, err
	}
	if p != nil {
		if err := p.Validate(); err != nil {
			return nil, ErrInvalidArgument
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	row, err := q.EventRoutingRetryLockTarget(ctx, tx, sqlc.EventRoutingRetryLockTargetParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub)})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if p != nil && p.MaxDeliveryAgeMS > 0 {
		ordered, err := q.EventAgeOrderedBinding(ctx, tx, mustPgUUID(sub))
		if err != nil {
			return nil, err
		}
		if ordered {
			return nil, ErrInvalidArgument
		}
	}
	old := decodeEventRoutingRetryPolicy(row.RoutingRetryPolicy)
	if len(expected) > 0 && !sameEventRoutingRetryPolicy(old, expected[0]) {
		return nil, ErrConflict
	}
	if sameEventRoutingRetryPolicy(old, p) {
		return old, tx.Commit(ctx)
	}
	var encoded []byte
	if p != nil {
		encoded, err = json.Marshal(p)
		if err != nil {
			return nil, err
		}
	}
	if err = q.EventRoutingRetrySet(ctx, tx, sqlc.EventRoutingRetrySetParams{SubscriptionID: mustPgUUID(sub), Policy: encoded}); err != nil {
		return nil, err
	}
	return old, tx.Commit(ctx)
}
func (m *MemStore) eventRoutingRetryTargetLocked(account, app, sub string) *EventSubscription {
	target, ok := m.eventSubscriptionAppLocked(app)
	if !ok || target.Status == AppDeleted || !sameMemUUID(target.AccountID, account) {
		return nil
	}
	for _, s := range m.eventSubscriptions {
		if sameMemUUID(s.ID, sub) && sameMemUUID(s.AppID, app) && sameMemUUID(s.AccountID, account) {
			copy := s
			return &copy
		}
	}
	return nil
}
func (m *MemStore) GetEventSubscriptionRetryPolicy(ctx context.Context, account, app, sub string) (api.EventRoutingRetryPolicyResponse, error) {
	if err := ctx.Err(); err != nil {
		return api.EventRoutingRetryPolicyResponse{}, err
	}
	if err := validSubscriptionControlIDs(account, app, sub); err != nil {
		return api.EventRoutingRetryPolicyResponse{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.eventRoutingRetryTargetLocked(account, app, sub)
	if s == nil {
		return api.EventRoutingRetryPolicyResponse{}, ErrNotFound
	}
	return eventRoutingRetryPolicyResponse(sub, s.RoutingRetryPolicy), nil
}
func (m *MemStore) SetEventSubscriptionRetryPolicy(ctx context.Context, account, app, sub string, p *api.EventRoutingRetryPolicy, expected ...*api.EventRoutingRetryPolicy) (*api.EventRoutingRetryPolicy, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validSubscriptionControlIDs(account, app, sub); err != nil {
		return nil, err
	}
	if p != nil {
		if err := p.Validate(); err != nil {
			return nil, ErrInvalidArgument
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.eventRoutingRetryTargetLocked(account, app, sub)
	if s == nil {
		return nil, ErrNotFound
	}
	if p != nil && p.MaxDeliveryAgeMS > 0 {
		for _, b := range m.eventWorkBindings {
			if sameMemUUID(b.SubscriptionID, sub) && b.Ordered {
				return nil, ErrInvalidArgument
			}
		}
	}
	old := cloneEventRoutingRetryPolicy(s.RoutingRetryPolicy)
	if len(expected) > 0 && !sameEventRoutingRetryPolicy(old, expected[0]) {
		return nil, ErrConflict
	}
	if !sameEventRoutingRetryPolicy(old, p) {
		s.RoutingRetryPolicy = cloneEventRoutingRetryPolicy(p)
		s.UpdatedAt = time.Now().UTC()
		for key, row := range m.eventSubscriptions {
			if sameMemUUID(row.ID, sub) {
				m.eventSubscriptions[key] = *s
				break
			}
		}
	}
	return old, nil
}
