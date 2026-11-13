package state

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
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

// EventFanoutFailureCodeDeliveryHeldExpired is the dead-letter code of a
// delivery held past api.EventDeliveryHoldMax because events or workflows were
// off for its app (ADR-20261027125625 amendment 2026-11-13).
const EventFanoutFailureCodeDeliveryHeldExpired = "delivery_held_expired"

var ErrEventDeliveryHeldExpired = errors.New("event delivery was held longer than the hold limit while events or workflows were off for its app")

// EventDeliveryHoldExpired reports whether a held delivery accepted at
// accepted has waited past api.EventDeliveryHoldMax. A zero acceptance never
// expires.
func EventDeliveryHoldExpired(accepted, now time.Time) bool {
	return !accepted.IsZero() && now.Sub(accepted) >= api.EventDeliveryHoldMax
}

// EventDeliveryHoldRecheck is when a held delivery is checked again: a tenth
// of how long it has waited, between api.EventDeliveryHoldRecheckMin and
// api.EventDeliveryHoldRecheckMax, so a long hold costs a claim an hour, not
// one a minute.
func EventDeliveryHoldRecheck(accepted, now time.Time) time.Time {
	wait := api.EventDeliveryHoldRecheckMin
	if !accepted.IsZero() {
		wait = min(max(now.Sub(accepted)/10, api.EventDeliveryHoldRecheckMin), api.EventDeliveryHoldRecheckMax)
	}
	if deadline := accepted.Add(api.EventDeliveryHoldMax); !accepted.IsZero() && now.Add(wait).After(deadline) {
		return deadline
	}
	return now.Add(wait)
}

// EventDeliveryHeldExpiredProgress moves a held delivery to the dead-letter
// queue; a replay once events are back on routes it normally.
func EventDeliveryHeldExpiredProgress(previous PublishedEventRecipientProgress, attempts int, now time.Time) PublishedEventRecipientProgress {
	p := previous
	p.State, p.Attempts, p.UpdatedAt = PublishedEventRecipientFailed, attempts, now
	p.FailureCode, p.RetryStopReason, p.LastError = EventFanoutFailureCodeDeliveryHeldExpired, "delivery_held_expired", ErrEventDeliveryHeldExpired.Error()
	p.Retryable, p.NextAttemptAt, p.CapacityScope, p.DeliveryControlReason = false, nil, "", ""
	return p
}

type EventFanoutAgeReplayStore interface {
	ReplayFailedPublishedEventRecipientWithAgeOverride(context.Context, string, string, string, string, string, bool) error
}
