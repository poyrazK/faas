package state

import (
	"context"
	"errors"
	"time"
)

var ErrEventDeliveryExpired = errors.New("event delivery age limit exceeded; replay requires allow_expired")

const EventFanoutFailureCodeDeliveryExpired = "delivery_expired"

// Age starts at platform acceptance, never the producer's envelope time.
func EventDeliveryDeadline(r PublishedEventRecipient, accepted time.Time, p PublishedEventRecipientProgress) time.Time {
	if accepted.IsZero() || len(r.Workflow) != 0 || r.ObjectNotification != nil || r.Work != nil && r.Work.Ordered || p.DeliveryAgeOverride || r.DeliveryAgeOverride {
		return time.Time{}
	}
	age := r.EffectiveRoutingRetryPolicy().MaxDeliveryAgeMS
	if age <= 0 {
		return time.Time{}
	}
	return accepted.Add(time.Duration(age) * time.Millisecond)
}
func EventDeliveryExpired(r PublishedEventRecipient, accepted time.Time, p PublishedEventRecipientProgress, now time.Time) bool {
	deadline := EventDeliveryDeadline(r, accepted, p)
	return !deadline.IsZero() && !deadline.After(now)
}
func EventDeliveryExpiredProgress(previous PublishedEventRecipientProgress, attempts int, now time.Time) PublishedEventRecipientProgress {
	p := previous
	p.State, p.Attempts, p.UpdatedAt = PublishedEventRecipientFailed, attempts, now
	p.FailureCode, p.RetryStopReason, p.LastError = EventFanoutFailureCodeDeliveryExpired, "delivery_expired", ErrEventDeliveryExpired.Error()
	p.Retryable, p.NextAttemptAt, p.CapacityScope, p.DeliveryControlReason = false, nil, "", ""
	return p
}

type EventFanoutAgeReplayStore interface {
	ReplayFailedPublishedEventRecipientWithAgeOverride(context.Context, string, string, string, string, string, bool) error
}
