package sched

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func eventRoutingRetryDelay(policy api.EventRoutingRetryPolicy, attempt int, seed string) time.Duration {
	ms := policy.InitialBackoffMS
	for i := 1; i < attempt && ms < policy.MaxBackoffMS; i++ {
		ms = min(ms*2, policy.MaxBackoffMS)
	}
	if policy.Jitter {
		sum := sha256.Sum256([]byte(seed))
		ms = 1 + int64(binary.BigEndian.Uint64(sum[:8])%uint64(ms))
	}
	return time.Duration(ms) * time.Millisecond
}

// The duration ledger reserves each scheduled backoff and charges only real
// routing attempts. Admission deferrals preserve it without adding wait time.
func eventRoutingRetryOutcome(recipient state.PublishedEventRecipient, previous state.PublishedEventRecipientProgress, total, attempts, capacity int, generation int64, matched bool, routeErr error, now time.Time, elapsed time.Duration, seed string) (state.PublishedEventRecipientProgress, time.Time) {
	p := state.PublishedEventRecipientProgress{DeliveryAgeOverride: previous.DeliveryAgeOverride, Attempts: total, CapacityDeferrals: capacity, UpdatedAt: now, RetryGeneration: generation}
	if previous.RetryGeneration == generation {
		p.RetrySpentMS = previous.RetrySpentMS
	}
	if errors.Is(routeErr, state.ErrEventDeliveryExpired) {
		return state.EventDeliveryExpiredProgress(previous, max(0, total-1), now), now
	}
	if routeErr == nil {
		p.State = state.PublishedEventRecipientFiltered
		if matched {
			p.State = state.PublishedEventRecipientEnqueued
		}
		return p, now
	}
	policy := recipient.EffectiveRoutingRetryPolicy()
	p.LastError = routeErr.Error()
	p.FailureCode, p.Retryable = eventFanoutFailureDetails(routeErr)
	p.RetrySpentMS += max(0, elapsed.Milliseconds())
	switch {
	case !matched && !p.Retryable || errors.Is(routeErr, state.ErrNotFound):
		p.RetryStopReason = "non_retryable"
	case attempts >= policy.MaxAttempts:
		p.RetryStopReason = "max_attempts"
	}
	delay := eventRoutingRetryDelay(policy, max(1, attempts), seed)
	if p.RetryStopReason == "" && policy.MaxRetryDurationMS > 0 && p.RetrySpentMS+delay.Milliseconds() > policy.MaxRetryDurationMS {
		p.RetryStopReason = "max_duration"
	}
	if p.RetryStopReason != "" {
		p.State = state.PublishedEventRecipientFailed
		return p, now
	}
	p.State = state.PublishedEventRecipientPending
	p.RetrySpentMS += delay.Milliseconds()
	next := now.Add(delay)
	p.NextAttemptAt = &next
	return p, next
}
func eventRoutingRetrySeed(id int64, sub string, generation int64, attempt int) string {
	return fmt.Sprintf("%d/%s/%d/%d", id, sub, generation, attempt)
}
