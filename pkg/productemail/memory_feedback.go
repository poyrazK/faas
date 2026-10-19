package productemail

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *Memory) pruneFeedback(now time.Time) {
	for key, f := range m.pending {
		if f.ReceivedAt.Before(now.Add(-api.ProductControlEmailPendingFeedbackTTL)) {
			delete(m.pending, key)
		}
	}
}
func (m *Memory) pause(d Delivery, now time.Time) {
	reason, _ := suppression(d)
	at := now
	if reason == "" {
		return
	}
	s := m.subscriptions[d.SubscriptionID]
	if reason == "hard_bounce" && d.Generation != s.Generation {
		return
	}
	if s.PauseReason == "" || reason == "complaint" && s.PauseReason != "complaint" {
		s.PauseReason, s.PausedAt = reason, at
		s.PauseRecipient = append([]byte{}, d.RecipientSealed...)
		m.subscriptions[d.SubscriptionID] = s
	}
	for id, pending := range m.deliveries {
		if pending.SubscriptionID == d.SubscriptionID && pending.Status == "queued" {
			pending.Status, pending.Reason, pending.UpdatedAt = "cancelled", "recipient_suppressed", now
			m.deliveries[id] = pending
		}
	}
}
func (m *Memory) Accept(ctx context.Context, d Delivery, providerID string, now time.Time) error {
	if !canonicalID(providerID) || len(d.RecipientSealed) > api.ProductControlEmailCipherMaxBytes {
		return ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	current := m.deliveries[d.ID]
	if current.Status != "leased" || current.Attempts != d.Attempts {
		return ErrConflict
	}
	for _, other := range m.deliveries {
		if other.ProviderID == providerID && other.ID != d.ID {
			return ErrConflict
		}
	}
	m.pruneFeedback(now)
	current.RecipientSealed = append([]byte{}, d.RecipientSealed...)
	current.Status, current.Reason, current.ProviderID, current.UpdatedAt = "provider_accepted", "", providerID, now
	for key, f := range m.pending {
		if f.ProviderID == providerID {
			current = applyFeedback(current, f.Feedback, now)
			delete(m.pending, key)
		}
	}
	m.deliveries[d.ID] = current
	m.pause(current, now)
	return nil
}
func (m *Memory) Feedback(ctx context.Context, f Feedback, now time.Time) error {
	if !f.valid(now) {
		return ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	m.pruneFeedback(now)
	for id, d := range m.deliveries {
		if d.ProviderID == f.ProviderID && d.CreatedAt.After(now.Add(-api.ProductControlNotificationWindow)) {
			d = applyFeedback(d, f, now)
			m.deliveries[id] = d
			m.pause(d, now)
			return nil
		}
	}
	key := f.ProviderID + "/" + f.Kind
	if old, ok := m.pending[key]; ok {
		if old.OccurredAt.Before(f.OccurredAt) {
			f.OccurredAt = old.OccurredAt
		}
		f.HardBounce = f.HardBounce || old.HardBounce
		m.pending[key] = pendingFeedback{Feedback: f, ReceivedAt: old.ReceivedAt}
		return nil
	}
	if len(m.pending) >= api.ProductControlEmailPendingFeedbackLimit {
		return ErrLimit
	}
	m.pending[key] = pendingFeedback{Feedback: f, ReceivedAt: now}
	return nil
}
