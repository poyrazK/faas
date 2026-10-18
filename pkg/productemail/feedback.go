package productemail

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Feedback retains only authenticated provider message metadata, never the raw
// webhook body, recipient address, subject, bounce text or tracking information.
type Feedback struct {
	ProviderID, Kind string
	OccurredAt       time.Time
	HardBounce       bool
}
type pendingFeedback struct {
	Feedback
	ReceivedAt time.Time
}

func cloneTime(at *time.Time) *time.Time {
	if at == nil {
		return nil
	}
	v := *at
	return &v
}
func cloneDelivery(d Delivery) Delivery {
	d.UnsubscribeHash = append([]byte{}, d.UnsubscribeHash...)
	d.UnsubscribeSealed = append([]byte{}, d.UnsubscribeSealed...)
	d.ReportedAt = cloneTime(d.ReportedAt)
	d.RecipientSealed = append([]byte{}, d.RecipientSealed...)
	d.DeliveredAt, d.BouncedAt, d.ComplainedAt = cloneTime(d.DeliveredAt), cloneTime(d.BouncedAt), cloneTime(d.ComplainedAt)
	return d
}

func (f Feedback) valid(now time.Time) bool {
	return canonicalID(f.ProviderID) && (f.Kind == "delivered" || f.Kind == "bounced" || f.Kind == "complained") && !f.OccurredAt.IsZero() && f.OccurredAt.After(now.Add(-api.ProductControlNotificationWindow)) && !f.OccurredAt.After(now.Add(api.ProductControlEmailFeedbackTolerance)) && (!f.HardBounce || f.Kind == "bounced")
}
func earlier(old *time.Time, at time.Time) *time.Time {
	if old == nil || at.Before(*old) {
		v := at.UTC()
		return &v
	}
	return old
}
func applyFeedback(d Delivery, f Feedback, now time.Time) Delivery {
	switch f.Kind {
	case "delivered":
		d.DeliveredAt = earlier(d.DeliveredAt, f.OccurredAt)
	case "bounced":
		d.BouncedAt = earlier(d.BouncedAt, f.OccurredAt)
		d.HardBounced = d.HardBounced || f.HardBounce
	case "complained":
		d.ComplainedAt = earlier(d.ComplainedAt, f.OccurredAt)
	}
	d.Status, d.ReportedAt = "provider_accepted", nil
	if d.DeliveredAt != nil {
		d.Status, d.ReportedAt = "delivered", d.DeliveredAt
	}
	if d.BouncedAt != nil {
		d.Status, d.ReportedAt = "bounced", d.BouncedAt
	}
	if d.ComplainedAt != nil {
		d.Status, d.ReportedAt = "complained", d.ComplainedAt
	}
	if now.After(d.UpdatedAt) {
		d.UpdatedAt = now
	}
	return d
}
func suppression(d Delivery) (string, time.Time) {
	if d.ComplainedAt != nil {
		return "complaint", *d.ComplainedAt
	}
	if d.HardBounced && d.BouncedAt != nil {
		return "hard_bounce", *d.BouncedAt
	}
	return "", time.Time{}
}
