package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) GetEventCircuitBreaker(ctx context.Context, account, app, sub string) (api.EventCircuitBreakerResponse, error) {
	if err := ctx.Err(); err != nil {
		return api.EventCircuitBreakerResponse{}, err
	}
	if err := validSubscriptionControlIDs(account, app, sub); err != nil {
		return api.EventCircuitBreakerResponse{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.eventSubscriptionControlTargetLocked(account, app, sub) {
		return api.EventCircuitBreakerResponse{}, ErrNotFound
	}
	paused := false
	if c := m.eventSubscriptionControls[canonicalMemUUID(sub)]; c != nil {
		paused = c.Paused
	}
	r := m.eventCircuitBreakers[canonicalMemUUID(sub)]
	m.refreshCircuitProbeLeaseLocked(r)
	return circuitResponse(sub, r, paused, time.Now().UTC()), nil
}
func (m *MemStore) SetEventCircuitBreaker(ctx context.Context, account, app, sub string, p *api.EventCircuitBreakerPolicy) (api.EventCircuitBreakerResponse, error) {
	return m.changeEventCircuit(ctx, account, app, sub, p, false)
}
func (m *MemStore) ResetEventCircuitBreaker(ctx context.Context, account, app, sub string) (api.EventCircuitBreakerResponse, error) {
	return m.changeEventCircuit(ctx, account, app, sub, nil, true)
}
func (m *MemStore) changeEventCircuit(ctx context.Context, account, app, sub string, p *api.EventCircuitBreakerPolicy, reset bool) (api.EventCircuitBreakerResponse, error) {
	out := api.EventCircuitBreakerResponse{}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if err := validSubscriptionControlIDs(account, app, sub); err != nil {
		return out, err
	}
	if p != nil {
		if err := p.Validate(); err != nil {
			return out, ErrInvalidArgument
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.eventSubscriptionControlTargetLocked(account, app, sub) {
		return out, ErrNotFound
	}
	if m.eventCircuitBreakers == nil {
		m.eventCircuitBreakers = map[string]*eventCircuitRecord{}
	}
	id := canonicalMemUUID(sub)
	r := m.eventCircuitBreakers[id]
	now := time.Now().UTC()
	if reset {
		if r == nil {
			return out, ErrNotFound
		}
		r.Runtime = newEventCircuitRuntime(now, "manual_reset")
	} else if p == nil {
		delete(m.eventCircuitBreakers, id)
		r = nil
	} else {
		r = &eventCircuitRecord{AccountID: canonicalMemUUID(account), AppID: canonicalMemUUID(app), SubscriptionID: id, Policy: *p, Runtime: newEventCircuitRuntime(now, "configured")}
		m.eventCircuitBreakers[id] = r
		if m.eventSubscriptionControls == nil {
			m.eventSubscriptionControls = map[string]*memEventSubscriptionControl{}
		}
		if m.eventSubscriptionControls[id] == nil {
			m.eventSubscriptionControls[id] = &memEventSubscriptionControl{AccountID: r.AccountID, AppID: r.AppID, UpdatedAt: now}
		}
	}
	paused := false
	if c := m.eventSubscriptionControls[id]; c != nil {
		paused = c.Paused
	}
	return circuitResponse(sub, r, paused, now), nil
}
func (m *MemStore) circuitObservationLocked(r *eventCircuitRecord, now time.Time) eventCircuitObservation {
	m.refreshCircuitProbeLeaseLocked(r)
	out := eventCircuitObservation{}
	if (r.Runtime.State == "closed" || r.Runtime.State == "draining") && now.Sub(r.Runtime.EvaluatedAt) >= api.EventCircuitEvaluationInterval {
		out.Sampled = true
		r.Runtime.EvaluatedAt = now
		since := circuitSince(r, now)
		receipts := map[int64]*PublishedEventWork{}
		for _, receipt := range m.eventFanout {
			receipts[receipt.ID] = receipt
		}
		for _, h := range m.eventFanoutAttempts {
			receipt := receipts[h.OutboxID]
			if receipt == nil || !sameMemUUID(eventRoutingAccount(receipt), r.AccountID) || !sameMemUUID(h.AppID, r.AppID) || !sameMemUUID(h.SubscriptionID, r.SubscriptionID) || h.OccurredAt.Before(since) || h.OccurredAt.After(now) || h.Action != EventFanoutAttemptActionAttempt && h.Action != EventFanoutAttemptActionBackfill {
				continue
			}
			if h.State == PublishedEventRecipientEnqueued {
				out.Successes++
			}
			if h.FailureCode != EventFanoutFailureCodeDeliveryExpired && (h.State == PublishedEventRecipientFailed || h.State == PublishedEventRecipientPending && h.LastError != "" && h.CapacityScope == "") {
				out.Failures++
			}
		}
		for key, h := range m.eventFanoutHistorySummaries {
			receipt := receipts[key.outboxID]
			if receipt != nil && sameMemUUID(eventRoutingAccount(receipt), r.AccountID) && sameMemUUID(h.SubscriptionID, r.SubscriptionID) && h.CompactedThroughAt != nil && !h.CompactedThroughAt.Before(since) {
				out.Incomplete = true
			}
		}
	}
	if r.Runtime.State == "half_open" && r.Runtime.ProbeToken != "" {
		if receipt := m.routingReceiptLocked(r.Runtime.ProbeOutboxID); receipt != nil {
			p := receipt.RecipientProgress[r.SubscriptionID]
			out.Probe = &p
		}
	}
	return out
}

func (m *MemStore) refreshCircuitProbeLeaseLocked(r *eventCircuitRecord) {
	if r == nil || r.Runtime.State != "half_open" || r.Runtime.ProbeToken == "" {
		return
	}
	if receipt := m.routingReceiptLocked(r.Runtime.ProbeOutboxID); receipt != nil {
		claim := receipt.routingRecipients[r.SubscriptionID]
		if claim != nil && claim.State == "processing" && claim.ClaimToken == r.Runtime.ProbeToken {
			r.Runtime.ProbeUntil = claim.LeaseUntil
		}
	}
}
func (m *MemStore) AcquireEventCircuitPermit(ctx context.Context, work *PublishedEventRecipientWork, now time.Time) (string, time.Time, error) {
	if err := ctx.Err(); err != nil {
		return "", time.Time{}, err
	}
	if !circuitWorkEligible(work) {
		return "", time.Time{}, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.eventCircuitBreakers[canonicalMemUUID(work.Recipient.ID)]
	if r == nil || !sameMemUUID(r.AccountID, work.Recipient.AccountID) || !sameMemUUID(r.AppID, work.Recipient.AppID) {
		return "", time.Time{}, nil
	}
	app, ok := m.eventSubscriptionAppLocked(r.AppID)
	if !ok || app.Status == AppDeleted || !sameMemUUID(app.AccountID, r.AccountID) {
		return "", time.Time{}, nil
	}
	if c := m.eventSubscriptionControls[r.SubscriptionID]; c != nil && c.Paused {
		return "subscription_paused", now.Add(api.EventSubscriptionControlRetryDelay), nil
	}
	circuitAdvance(r, m.circuitObservationLocked(r, now), now)
	reason, next := circuitWaitingReason(r, now)
	if reason == "" {
		circuitReserve(r, work, now)
	}
	return reason, next, nil
}
func (m *MemStore) observeCircuitLocked(work *PublishedEventWork, sub string, p PublishedEventRecipientProgress) {
	r := m.eventCircuitBreakers[canonicalMemUUID(sub)]
	if r == nil {
		return
	}
	observation := m.circuitObservationLocked(r, p.UpdatedAt)
	if r.Runtime.State == "half_open" && r.Runtime.ProbeOutboxID == work.ID {
		observation.Probe = &p
	}
	if r.Runtime.State == "draining" && circuitOutcomeFailed(p) {
		observation.Failures = max(1, observation.Failures)
	}
	circuitAdvance(r, observation, p.UpdatedAt)
}
func EventCircuitWaitProgress(previous PublishedEventRecipientProgress, total int, reason string, next time.Time) PublishedEventRecipientProgress {
	return eventSubscriptionControlProgress(previous, total, reason, next)
}

// EventCapabilityWaitReason marks a recipient held while the events
// capability is off for its app's account (ADR-20261027125625).
const EventCapabilityWaitReason = "events_paused"

// EventCapabilityWaitProgress holds a recipient until next without spending
// the routing attempt its claim took.
func EventCapabilityWaitProgress(previous PublishedEventRecipientProgress, total int, next time.Time) PublishedEventRecipientProgress {
	return eventSubscriptionControlProgress(previous, total, EventCapabilityWaitReason, next)
}

// EventWorkflowWaitProgress holds a workflow recipient without spending an
// attempt while its capability or captured plan requirements are unavailable.
func EventWorkflowWaitProgress(previous PublishedEventRecipientProgress, total int, reason string, next time.Time) PublishedEventRecipientProgress {
	return eventSubscriptionControlProgress(previous, total, reason, next)
}

const EventWorkflowCapabilityWaitReason = "workflows_paused"
const EventWorkflowPlanWaitReason = "workflow_plan_ineligible"
