package productemail

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type Memory struct {
	mu            sync.Mutex
	subscriptions map[string]Subscription
	deliveries    map[string]Delivery
	retries       map[string]api.ProductEmailRetryReceipt
	pending       map[string]pendingFeedback
	recoveries    map[string]api.ProductEmailRecoveryReceipt
	consent       map[string]*consentGate
	optOuts       map[string]api.ProductEmailOptOutReceipt
}

func NewMemory() *Memory {
	return &Memory{subscriptions: map[string]Subscription{}, deliveries: map[string]Delivery{}, retries: map[string]api.ProductEmailRetryReceipt{}, pending: map[string]pendingFeedback{}, recoveries: map[string]api.ProductEmailRecoveryReceipt{}, consent: map[string]*consentGate{}, optOuts: map[string]api.ProductEmailOptOutReceipt{}}
}
func clone(s Subscription) Subscription {
	s.Grant = append([]byte(nil), s.Grant...)
	s.PauseRecipient = append([]byte{}, s.PauseRecipient...)
	return s
}
func (m *Memory) Get(ctx context.Context, k Key) (Subscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := clone(m.subscriptions[k.ID()])
	s.Key = k
	return s, ctx.Err()
}
func (m *Memory) prune(now time.Time) {
	for id, d := range m.deliveries {
		if d.CreatedAt.Before(now.Add(-api.ProductControlNotificationWindow)) {
			delete(m.deliveries, id)
		}
	}
	for key, receipt := range m.retries {
		if _, ok := m.deliveries[receipt.DeliveryID]; !ok {
			delete(m.retries, key)
		}
	}
	for id, receipt := range m.optOuts {
		if _, ok := m.deliveries[receipt.DeliveryID]; !ok {
			delete(m.optOuts, id)
		}
	}
}
func (m *Memory) Put(ctx context.Context, s Subscription, expected int64, now time.Time) (Subscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return s, err
	}
	old := m.subscriptions[s.ID()]
	next, err := update(old, s, expected, now)
	if err != nil {
		return s, err
	}
	if old.Version == 0 {
		count := 0
		for _, v := range m.subscriptions {
			if v.AccountID == s.AccountID && v.AppID == s.AppID && v.TenantID == s.TenantID {
				count++
			}
		}
		if count >= api.ProductControlEmailMaxSubscribers {
			return s, ErrLimit
		}
	}
	m.subscriptions[s.ID()] = clone(next)
	for id, d := range m.deliveries {
		if d.SubscriptionID == s.ID() && d.Status == "queued" {
			d.Status = "cancelled"
			d.Reason = "preferences_changed"
			d.UpdatedAt = now
			m.deliveries[id] = d
		}
	}
	return clone(next), nil
}
func (m *Memory) List(ctx context.Context, after string, now time.Time) ([]Subscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.prune(now)
	out := []Subscription{}
	for id, s := range m.subscriptions {
		if id > after && s.PauseReason == "" && s.ExpiresAt.After(now) && (s.Work || s.Help) {
			out = append(out, clone(s))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	if len(out) > api.ProductControlEmailBatchSize {
		out = out[:api.ProductControlEmailBatchSize]
	}
	return out, ctx.Err()
}
func (m *Memory) Progress(ctx context.Context, id string, version int64, cursor string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.subscriptions[id]
	if s.Version != version {
		return ErrConflict
	}
	s.Cursor = cursor
	m.subscriptions[id] = s
	return ctx.Err()
}
func (m *Memory) Enqueue(ctx context.Context, s Subscription, items []api.ProductNotification, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.prune(now)
	current := m.subscriptions[s.ID()]
	if current.Version != s.Version {
		return ErrConflict
	}
	count := 0
	for _, d := range m.deliveries {
		if d.SubscriptionID == s.ID() {
			count++
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	pending := map[string]Delivery{}
	for _, n := range items {
		if !Enabled(current, n, now) {
			continue
		}
		d := newDelivery(s, n, now)
		if _, exists := m.deliveries[d.ID]; exists {
			continue
		}
		if _, exists := pending[d.ID]; exists {
			continue
		}
		if count >= api.ProductControlEmailMaxDeliveries {
			return ErrLimit
		}
		pending[d.ID] = d
		count++
	}
	for id, d := range pending {
		m.deliveries[id] = d
	}
	return ctx.Err()
}
func (m *Memory) Claim(ctx context.Context, now time.Time) ([]Delivery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Delivery{}
	m.prune(now)
	for id, d := range m.deliveries {
		if !terminal(d.Status) && !d.NextAt.After(now) {
			if !d.Deadline.After(now) || d.Attempts >= api.ProductControlEmailMaxAttempts {
				d.Status = "failed"
				d.Reason = "retry_exhausted"
				d.UpdatedAt = now
				m.deliveries[id] = d
				continue
			}
			out = append(out, cloneDelivery(d))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].NextAt.Equal(out[j].NextAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].NextAt.Before(out[j].NextAt)
	})
	if len(out) > api.ProductControlEmailClaimSize {
		out = out[:api.ProductControlEmailClaimSize]
	}
	for i := range out {
		d := &out[i]
		d.Status = "leased"
		d.Attempts++
		d.UpdatedAt = now
		d.NextAt = now.Add(api.ProductControlEmailLease)
		m.deliveries[d.ID] = cloneDelivery(*d)
	}
	return out, ctx.Err()
}
func (m *Memory) Ready(ctx context.Context, d Delivery, now time.Time) (Subscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.deliveries[d.ID]
	s := m.subscriptions[d.SubscriptionID]
	if s.PauseReason != "" {
		return clone(s), ErrSuppressed
	}
	if current.Status != "leased" || current.Attempts != d.Attempts || !current.NextAt.After(now) || !current.Deadline.After(now) || s.Version != d.Version || !Enabled(s, d.Notification, now) {
		return s, ErrConflict
	}
	return clone(s), ctx.Err()
}
func (m *Memory) Finish(ctx context.Context, d Delivery, status, reason string, next, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.deliveries[d.ID]
	if !validFinish(status, reason) || current.Status != "leased" || current.Attempts != d.Attempts {
		return ErrConflict
	}
	current.Status, current.Reason, current.NextAt, current.UpdatedAt = status, reason, next, now
	m.deliveries[d.ID] = current
	return ctx.Err()
}
func (m *Memory) Recent(ctx context.Context, k Key) ([]api.ProductEmailDelivery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.prune(time.Now().UTC())
	out := []api.ProductEmailDelivery{}
	for _, d := range m.deliveries {
		if d.SubscriptionID == k.ID() {
			v := d.ProductEmailDelivery
			v.ReportedAt = cloneTime(v.ReportedAt)
			if v.Status == "leased" {
				v.Status = "queued"
			}
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if len(out) > api.ProductControlEmailBatchSize {
		out = out[:api.ProductControlEmailBatchSize]
	}
	return out, ctx.Err()
}
