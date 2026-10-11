package state

import (
	"context"
	"errors"
	"time"
)

func (m *MemStore) AdmitEventWorkflowRecipient(_ context.Context, claim PublishedEventRoutingClaim) (result EventWorkflowRoutingResult, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	receipt := m.routingReceiptLocked(claim.OutboxID)
	if receipt == nil {
		return result, ErrNotFound
	}
	recipient, matched, err := workflowRoutingRecipient(receipt, claim)
	result.Matched = matched
	if err != nil {
		return result, workflowRoutingAdmissionError(err)
	}
	r := receipt.routingRecipients[recipient.ID]
	if r == nil || r.Generation != claim.Generation {
		return result, ErrConflict
	}
	previous := receipt.RecipientProgress[recipient.ID]
	if routingAdmissionRecorded(previous) {
		result.RunID = m.retainedEventWorkflowRunIDLocked(receipt.ID, recipient.ID)
		result.Progress, result.ReceiptSettled = previous, receipt.Delivered
		return result, nil
	}
	if !eventRoutingClaimValidLocked(receipt, r, claim) {
		return result, ErrConflict
	}
	if matched {
		result.RunID, result.RunCreated, err = m.admitEventWorkflowLocked(receipt, recipient)
		if err != nil {
			if errors.Is(err, ErrWorkflowEventDefinitionInvalid) {
				result.Matched = false
			}
			return result, workflowRoutingAdmissionError(err)
		}
	}
	if !eventRoutingClaimValidLocked(receipt, r, claim) {
		if result.RunCreated {
			delete(m.workflowRuns, result.RunID)
			delete(m.eventWorkflowReceipts, eventWorkflowReceiptKey(receipt.ID, recipient.ID))
		}
		return EventWorkflowRoutingResult{Matched: matched}, ErrConflict
	}
	progress := workflowRoutingProgress(matched, r.TotalAttempts)
	receipt.RecipientProgress[recipient.ID] = progress
	m.appendEventFanoutAttemptLocked(receipt, recipient.ID, EventFanoutAttemptActionAttempt, progress)
	r.State, r.ClaimToken, r.LeaseUntil = progress.State, "", time.Time{}
	settleEventRecipientsLocked(receipt, progress.UpdatedAt)
	result.Progress, result.ReceiptSettled = progress, receipt.Delivered
	return result, nil
}

func (m *MemStore) retainedEventWorkflowRunIDLocked(outboxID int64, recipientID string) string {
	id := m.eventWorkflowReceipts[eventWorkflowReceiptKey(outboxID, recipientID)]
	if _, retained := m.workflowRuns[id]; !retained {
		return ""
	}
	return id
}
