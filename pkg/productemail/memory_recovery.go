package productemail

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *Memory) pruneRecoveries(now time.Time) {
	for id, r := range m.recoveries {
		if !r.RecoveredAt.After(now.Add(-api.ProductControlNotificationWindow)) {
			delete(m.recoveries, id)
		}
	}
}
func (m *Memory) RecoveryReceipt(ctx context.Context, k Key, id string, now time.Time) (api.ProductEmailRecoveryReceipt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var v api.ProductEmailRecoveryReceipt
	if !canonicalID(id) {
		return v, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return v, err
	}
	m.pruneRecoveries(now)
	v, ok := m.recoveries[id]
	if !ok || v.RecipientID != k.ID() {
		return api.ProductEmailRecoveryReceipt{}, ErrNotFound
	}
	return v, nil
}
func (m *Memory) Recover(ctx context.Context, next Subscription, r api.ProductEmailRecoveryRequest, now time.Time) (api.ProductEmailRecoveryReceipt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var v api.ProductEmailRecoveryReceipt
	now = now.UTC().Truncate(time.Microsecond)
	if !ValidRecoveryRequest(r) {
		return v, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return v, err
	}
	m.pruneRecoveries(now)
	if v, ok := m.recoveries[r.ID]; ok {
		if v.RecipientID != next.ID() || !MatchesRecovery(v, r) {
			return api.ProductEmailRecoveryReceipt{}, ErrConflict
		}
		return v, nil
	}
	count := 0
	for _, v := range m.recoveries {
		if v.RecipientID == next.ID() {
			count++
		}
	}
	if count >= api.ProductControlEmailRecoveryLimit {
		return v, ErrLimit
	}
	next, v, err := recovered(m.subscriptions[next.ID()], next, r, now)
	if err != nil {
		return v, err
	}
	m.subscriptions[next.ID()] = clone(next)
	m.recoveries[v.ID] = v
	for id, d := range m.deliveries {
		if d.SubscriptionID == next.ID() && d.Status == "queued" {
			d.Status, d.Reason, d.UpdatedAt = "cancelled", "recipient_suppressed", now
			m.deliveries[id] = d
		}
	}
	return v, nil
}
func (m *Memory) Recoveries(ctx context.Context, s OwnerScope, recipient, cursor string, now time.Time) (api.ProductEmailRecoveryPage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := newRecoveryPage(s, recipient, now)
	before, err := decodeRecoveryCursor(s, recipient, cursor)
	if err != nil {
		return p, err
	}
	if err = ctx.Err(); err != nil {
		return p, err
	}
	m.pruneRecoveries(now)
	for _, v := range m.recoveries {
		sub := m.subscriptions[v.RecipientID]
		if !s.includes(sub) || recipient != "" && v.RecipientID != recipient || before.ID != "" && (v.RecoveredAt.After(before.At) || v.RecoveredAt.Equal(before.At) && v.ID >= before.ID) {
			continue
		}
		p.Receipts = append(p.Receipts, v)
	}
	sort.Slice(p.Receipts, func(i, j int) bool {
		a, b := p.Receipts[i], p.Receipts[j]
		if a.RecoveredAt.Equal(b.RecoveredAt) {
			return a.ID > b.ID
		}
		return a.RecoveredAt.After(b.RecoveredAt)
	})
	finishRecoveryPage(s, &p)
	return p, nil
}
