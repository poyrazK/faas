package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/eventcontract"
)

// EventWorkflowRecipientAdmissionStore commits a workflow run, its durable
// deduplication receipt, routing checkpoint and history under a recipient lease.
type EventWorkflowRecipientAdmissionStore interface {
	AdmitEventWorkflowRecipient(context.Context, PublishedEventRoutingClaim) (EventWorkflowRoutingResult, error)
}

type EventWorkflowRoutingResult struct {
	Matched        bool
	RunID          string
	RunCreated     bool
	Progress       PublishedEventRecipientProgress
	ReceiptSettled bool
}

func workflowRoutingRecipient(receipt *PublishedEventWork, claim PublishedEventRoutingClaim) (PublishedEventRecipient, bool, error) {
	if claim.Generation <= 0 || claim.BackfillJobID != "" {
		return PublishedEventRecipient{}, false, ErrInvalidArgument
	}
	recipient, err := capturedRoutingRecipient(receipt, claim)
	if err != nil {
		return recipient, false, err
	}
	if len(recipient.Workflow) == 0 || recipient.ObjectNotification != nil {
		return recipient, false, ErrInvalidArgument
	}
	var envelope eventcontract.Envelope
	if err := json.Unmarshal(receipt.Payload, &envelope); err != nil {
		return recipient, false, err
	}
	if err := envelope.Validate(); err != nil {
		return recipient, false, err
	}
	if !sameMemUUID(envelope.AccountID, recipient.AccountID) {
		return recipient, false, admissionError(EventFanoutFailureCodeTargetUnavailable, false, ErrNotFound)
	}
	matched, err := (eventcontract.Subscription{ID: recipient.ID, AccountID: recipient.AccountID,
		Source: recipient.Source, Type: recipient.Type, Filter: recipient.Filter}).Match(envelope)
	if err != nil {
		err = admissionError(EventFanoutFailureCodeInvalidSubscription, false, err)
	}
	return recipient, matched, err
}

func workflowRoutingAdmissionError(err error) error {
	if err == nil || errors.Is(err, ErrConflict) {
		return err
	}
	var classified *EventRecipientAdmissionError
	if errors.As(err, &classified) {
		return err
	}
	code, retryable := EventFanoutFailureCodeInvocationEnqueueFailed, true
	switch {
	case errors.Is(err, ErrNotFound):
		code, retryable = EventFanoutFailureCodeTargetUnavailable, false
	case errors.Is(err, ErrWorkflowEventTargetUnavailable):
		code = EventFanoutFailureCodeTargetUnavailable
	case errors.Is(err, ErrWorkflowEventDefinitionInvalid):
		code, retryable = EventFanoutFailureCodeInvalidSubscription, false
	}
	return admissionError(code, retryable, fmt.Errorf("admit workflow event recipient: %w", err))
}

func workflowRoutingProgress(matched bool, attempts int) PublishedEventRecipientProgress {
	state := PublishedEventRecipientFiltered
	if matched {
		state = PublishedEventRecipientEnqueued
	}
	return PublishedEventRecipientProgress{State: state, Attempts: attempts, UpdatedAt: time.Now().UTC()}
}
