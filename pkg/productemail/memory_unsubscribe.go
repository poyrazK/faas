package productemail

import (
	"bytes"
	"context"
	"crypto/sha256"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *Memory) PrepareUnsubscribe(ctx context.Context, d Delivery, hash, sealed []byte, now time.Time) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !validUnsubscribeMaterial(hash, sealed) {
		return nil, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	current := m.deliveries[d.ID]
	s := m.subscriptions[current.SubscriptionID]
	if current.Status != "leased" || current.Attempts != d.Attempts || !current.NextAt.After(now) || !current.Deadline.After(now) || s.Version != current.Version || !Enabled(s, current.Notification, now) {
		return nil, ErrConflict
	}
	if len(current.UnsubscribeSealed) == 0 {
		for _, other := range m.deliveries {
			if bytes.Equal(other.UnsubscribeHash, hash) {
				return nil, ErrConflict
			}
		}
		current.UnsubscribeHash, current.UnsubscribeSealed = append([]byte{}, hash...), append([]byte{}, sealed...)
		m.deliveries[d.ID] = current
	}
	return append([]byte{}, current.UnsubscribeSealed...), nil
}
func (m *Memory) unsubscribeDelivery(hash []byte, now time.Time) (Subscription, Delivery, error) {
	for _, d := range m.deliveries {
		if bytes.Equal(d.UnsubscribeHash, hash) && d.CreatedAt.After(now.Add(-api.ProductControlNotificationWindow)) {
			return m.subscriptions[d.SubscriptionID], d, nil
		}
	}
	return Subscription{}, Delivery{}, ErrNotFound
}
func (m *Memory) Unsubscribe(ctx context.Context, hash []byte, now time.Time) (api.ProductEmailUnsubscribeResult, error) {
	var out api.ProductEmailUnsubscribeResult
	if len(hash) != sha256.Size {
		return out, ErrInvalid
	}
	now = now.UTC().Truncate(time.Microsecond)
	m.mu.Lock()
	s, _, err := m.unsubscribeDelivery(hash, now)
	m.mu.Unlock()
	if err != nil {
		return out, err
	}
	err = m.WithConsent(ctx, s.Key, func() error {
		m.mu.Lock()
		defer m.mu.Unlock()
		if e := ctx.Err(); e != nil {
			return e
		}
		m.prune(now)
		current, d, e := m.unsubscribeDelivery(hash, now)
		if e != nil {
			return e
		}
		if v, ok := m.optOuts[unsubscribeReceiptID(current.ID(), d.Version)]; ok {
			out = unsubscribeResult(v, current)
			return nil
		}
		next, v, e := unsubscribe(current, d, now)
		if e != nil {
			return e
		}
		m.subscriptions[next.ID()], m.optOuts[v.ID] = clone(next), v
		for id, pending := range m.deliveries {
			if pending.SubscriptionID == next.ID() && pending.Status == "queued" {
				pending.Status, pending.Reason, pending.UpdatedAt = "cancelled", "unsubscribed", now
				m.deliveries[id] = pending
			}
		}
		out = unsubscribeResult(v, next)
		return nil
	})
	return out, err
}
func (m *Memory) OptOuts(ctx context.Context, s OwnerScope, recipient, cursor string, now time.Time) (api.ProductEmailOptOutPage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := newOptOutPage(s, recipient, now)
	before, err := decodeOptOutCursor(s, recipient, cursor)
	if err != nil {
		return p, err
	}
	if err = ctx.Err(); err != nil {
		return p, err
	}
	m.prune(now)
	for _, v := range m.optOuts {
		sub := m.subscriptions[v.RecipientID]
		d := m.deliveries[v.DeliveryID]
		if !s.includes(sub) || !d.CreatedAt.After(p.WindowStart) || !v.UnsubscribedAt.After(p.WindowStart) || recipient != "" && v.RecipientID != recipient || before.ID != "" && (v.UnsubscribedAt.After(before.At) || v.UnsubscribedAt.Equal(before.At) && v.ID >= before.ID) {
			continue
		}
		p.Receipts = append(p.Receipts, v)
	}
	sort.Slice(p.Receipts, func(i, j int) bool {
		a, b := p.Receipts[i], p.Receipts[j]
		if a.UnsubscribedAt.Equal(b.UnsubscribedAt) {
			return a.ID > b.ID
		}
		return a.UnsubscribedAt.After(b.UnsubscribedAt)
	})
	finishOptOutPage(s, &p)
	return p, nil
}
