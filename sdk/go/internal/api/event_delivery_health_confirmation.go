package api

import (
	"math"
	"time"
)

// Confirmation diagnostics describe local observations and never restore proof.
type EventDeliveryHealthConfirmation struct {
	Metric                     string     `json:"metric"`
	Phase                      string     `json:"phase"`
	Reason                     string     `json:"reason"`
	FreshSamples               int        `json:"fresh_samples"`
	RequiredSamples            int        `json:"required_samples"`
	ElapsedSeconds             float64    `json:"elapsed_seconds"`
	RequiredSeconds            float64    `json:"required_seconds"`
	RemainingSeconds           float64    `json:"remaining_seconds"`
	ObservedAt                 *time.Time `json:"observed_at,omitempty"`
	LastConfirmationLossReason string     `json:"last_confirmation_loss_reason,omitempty"`
	LastConfirmationLossAt     *time.Time `json:"last_confirmation_loss_at,omitempty"`
}

func deliveryHealthConfirmationFailure(value float64, valid bool, observed, now time.Time, maxAge time.Duration, last time.Time) string {
	if !valid {
		return "evidence_unavailable"
	}
	if observed.IsZero() {
		return "missing_observation"
	}
	if observed.After(now) {
		return "future_observation"
	}
	if now.Sub(observed) > maxAge {
		return "stale_observation"
	}
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return "invalid_value"
	}
	if !last.IsZero() && !observed.After(last) {
		if observed.Equal(last) {
			return "repeated_observation"
		}
		return "out_of_order_observation"
	}
	return ""
}

func deliveryHealthReceiptBudgetConfirmation(c *deliveryHealthCondition, blocked bool) EventDeliveryHealthConfirmation {
	d := EventDeliveryHealthConfirmation{Metric: c.Metric, Phase: c.State, Reason: c.State, LastConfirmationLossReason: c.confirmationLossReason}
	if !c.confirmationLossAt.IsZero() {
		at := c.confirmationLossAt
		d.LastConfirmationLossAt = &at
	}
	if !c.localLast.IsZero() {
		at := c.observedAt
		d.ObservedAt = &at
	}
	if c.resuming {
		d.Phase = "resume_pending"
		d.Reason = "fresh_confirmation_required"
	}
	if c.State == "unknown" && !c.resuming {
		d.Reason = c.confirmationLossReason
		if d.Reason == "" {
			d.Reason = "awaiting_observation"
		}
	}
	if c.State == "firing" && c.RecoverySustainSeconds > 0 {
		d.Reason = "recovery_boundary_not_confirmed"
	}
	if !c.recoveryFirst.IsZero() && c.recoverySamples > 0 {
		d.Phase = "recovery_pending"
		d.Reason = "recovery_confirmation_pending"
		setDeliveryHealthConfirmationWindow(&d, c.recoverySamples, c.recoveryFirst, c.localLast, c.RecoverySustainSeconds)
		return d
	}
	if !c.first.IsZero() && c.samples > 0 {
		required := c.SustainSeconds
		if c.sustained {
			required = c.Threshold
		}
		setDeliveryHealthConfirmationWindow(&d, c.samples, c.first, c.localLast, required)
		if c.State != "firing" {
			d.Phase = "warning_pending"
			d.Reason = "warning_confirmation_pending"
			if blocked && d.RemainingSeconds == 0 && d.FreshSamples >= d.RequiredSamples {
				d.Phase = "notification_blocked"
				d.Reason = "alert_outbox_full"
			}
		}
	}
	return d
}

func setDeliveryHealthConfirmationWindow(d *EventDeliveryHealthConfirmation, samples int, first, last time.Time, required float64) {
	d.FreshSamples = samples
	d.RequiredSamples = 2
	d.ElapsedSeconds = max(0, last.Sub(first).Seconds())
	d.RequiredSeconds = required
	d.RemainingSeconds = max(0, required-d.ElapsedSeconds)
}
