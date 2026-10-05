package state

import (
	"context"
	"encoding/json"
	"maps"
	"time"
)

func (m *MemStore) routingReceiptLocked(id int64) *PublishedEventWork {
	for _, r := range m.eventFanout {
		if r.ID == id {
			return r
		}
	}
	return nil
}

func (m *MemStore) AdmitPublishedEventRecipient(ctx context.Context, claim PublishedEventRoutingClaim) (result PublishedEventRoutingResult, err error) {
	m.mu.Lock()
	receipt := m.routingReceiptLocked(claim.OutboxID)
	if receipt == nil {
		m.mu.Unlock()
		return PublishedEventRoutingResult{}, ErrNotFound
	}
	// Materialization may consult live bindings for old snapshots; release the
	// mutex for those reads, then revalidate authority under it before mutation.
	encoded, err := json.Marshal(receipt)
	m.mu.Unlock()
	if err != nil {
		return PublishedEventRoutingResult{}, err
	}
	var snapshot PublishedEventWork
	if err := json.Unmarshal(encoded, &snapshot); err != nil {
		return PublishedEventRoutingResult{}, err
	}
	plan, err := newEventAdmissionPlan(ctx, &snapshot, claim)
	if err != nil {
		return eventAdmissionResult(plan, PublishedEventRecipientProgress{}, false, false), err
	}
	defer func() {
		if err != nil && result.AppID == "" {
			result = eventAdmissionResult(plan, PublishedEventRecipientProgress{}, false, false)
		}
	}()
	if err := prepareEventAdmission(ctx, m, &snapshot, &plan); err != nil {
		return eventAdmissionResult(plan, PublishedEventRecipientProgress{}, false, false), eventAdmissionPrepareError(err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.admitEventRecipientLocked(claim, plan)
}

func (m *MemStore) admitEventRecipientLocked(claim PublishedEventRoutingClaim, p eventAdmissionPlan) (PublishedEventRoutingResult, error) {
	receipt := m.routingReceiptLocked(claim.OutboxID)
	if _, err := routingRecipient(receipt, claim); err != nil {
		return PublishedEventRoutingResult{}, err
	}
	r := receipt.routingRecipients[claim.SubscriptionID]
	if receipt.RecipientClaims && (r == nil || r.Generation != claim.Generation) {
		return PublishedEventRoutingResult{}, ErrConflict
	}
	if p.matched {
		app, ok := m.apps[p.recipient.AppID]
		if !ok {
			app, ok = m.apps[canonicalMemUUID(p.recipient.AppID)]
		}
		if !ok || app.Status == AppDeleted || !sameMemUUID(app.AccountID, p.recipient.AccountID) {
			return eventAdmissionResult(p, PublishedEventRecipientProgress{}, false, false), admissionError(EventFanoutFailureCodeTargetUnavailable, false, ErrNotFound)
		}
	}
	previous := receipt.RecipientProgress[claim.SubscriptionID]
	if routingAdmissionRecorded(previous) {
		return eventAdmissionResult(p, previous, false, receipt.Delivered), nil
	}
	if !eventRoutingClaimValidLocked(receipt, r, claim) {
		return PublishedEventRoutingResult{}, ErrConflict
	}
	// Stage the maps changed by enqueue, supersession and cancellation. Restore
	// them if a helper fails or the lease expires while applying its mutations.
	invocations, cancellations, history, next := m.invocations, m.workCancellations, m.invocationAttemptHistory, m.nextInvocationAttemptID
	m.invocations, m.workCancellations, m.invocationAttemptHistory = maps.Clone(invocations), maps.Clone(cancellations), maps.Clone(history)
	created, err := m.performEventAdmissionLocked(receipt, p)
	if err == nil && !eventRoutingClaimValidLocked(receipt, r, claim) {
		err = ErrConflict
	}
	if err != nil {
		m.invocations, m.workCancellations, m.invocationAttemptHistory, m.nextInvocationAttemptID = invocations, cancellations, history, next
		return eventAdmissionResult(p, PublishedEventRecipientProgress{}, false, false), admissionError(EventFanoutFailureCodeInvocationEnqueueFailed, true, err)
	}
	attempts := previous.Attempts + 1
	if r != nil {
		attempts = r.TotalAttempts
	}
	progress := eventAdmissionProgress(p, attempts)
	if receipt.RecipientProgress == nil {
		receipt.RecipientProgress = make(map[string]PublishedEventRecipientProgress)
	}
	receipt.RecipientProgress[claim.SubscriptionID] = progress
	m.appendEventFanoutAttemptLocked(receipt, claim.SubscriptionID, EventFanoutAttemptActionAttempt, progress)
	if receipt.RecipientClaims {
		r.State, r.ClaimToken, r.LeaseUntil = progress.State, "", time.Time{}
		r.AvailableAt = progress.UpdatedAt
		settleEventRecipientsLocked(receipt, progress.UpdatedAt)
	} else {
		receipt.Delivered = true
		for _, recipient := range receipt.RecipientSnapshot {
			state := receipt.RecipientProgress[recipient.ID].State
			if state != PublishedEventRecipientEnqueued && state != PublishedEventRecipientFiltered && state != PublishedEventRecipientFailed {
				receipt.Delivered = false
				break
			}
		}
		if receipt.Delivered {
			receipt.DeliveredAt = progress.UpdatedAt
			receipt.ClaimToken = ""
			receipt.LeaseUntil = time.Time{}
		}
	}
	return eventAdmissionResult(p, progress, created, receipt.Delivered), nil
}

func eventRoutingClaimValidLocked(receipt *PublishedEventWork, r *PublishedEventRecipientWork, claim PublishedEventRoutingClaim) bool {
	now := time.Now().UTC()
	if receipt.RecipientClaims {
		return r != nil && r.State == "processing" && r.Generation == claim.Generation && r.ClaimToken == claim.ClaimToken && r.LeaseUntil.After(now)
	}
	return !receipt.Delivered && receipt.ClaimToken == claim.ClaimToken && receipt.LeaseUntil.After(now)
}

func (m *MemStore) performEventAdmissionLocked(receipt *PublishedEventWork, p eventAdmissionPlan) (bool, error) {
	if !p.matched || p.prior {
		return false, nil
	}
	if p.cancel {
		cancellation, err := m.cancelPendingKeyedInvocationsLocked(p.invocation.AppID, p.invocation.WorkPolicyName, p.key, p.invocation.ID)
		if err == nil && cancellation.CreatedAt.Before(receipt.CreatedAt) {
			return false, ErrConflict
		}
		return false, err
	}
	if p.invocation.WorkPolicyName != "" {
		_, existing := m.invocations[p.invocation.ID]
		inv, err := m.enqueueKeyedInvocationLocked(p.invocation, p.policy, p.key, p.fairness...)
		if err == nil && existing && !priorEventInvocationMatches(receipt, p, inv) {
			return false, ErrConflict
		}
		return err == nil && !existing, err
	}
	_, err := m.enqueueInvocationLocked(p.invocation)
	return err == nil, err
}
