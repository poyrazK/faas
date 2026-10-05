package sched

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

func (l *Loop) routeWorkflowEvent(ctx context.Context, work *state.PublishedEventWork, envelope events.Envelope, recipient state.PublishedEventRecipient) (bool, error) {
	matched, err := (events.Subscription{ID: recipient.ID, AccountID: recipient.AccountID,
		Source: recipient.Source, Type: recipient.Type, Filter: recipient.Filter}).Match(envelope)
	if err != nil {
		return false, &eventFanoutRouteError{code: state.EventFanoutFailureCodeInvalidSubscription, err: err}
	}
	if !matched {
		return false, nil
	}
	store, ok := l.engine.store.(state.EventWorkflowStore)
	if !ok {
		return true, &eventFanoutRouteError{code: state.EventFanoutFailureCodeInternal, retryable: true,
			err: errors.New("workflow event store is unavailable")}
	}
	_, err = store.AdmitEventWorkflow(ctx, work.ID, work.ClaimToken, recipient.ID)
	if err == nil {
		return true, nil
	}
	code, retryable := state.EventFanoutFailureCodeInvocationEnqueueFailed, true
	if errors.Is(err, state.ErrNotFound) {
		code, retryable = state.EventFanoutFailureCodeTargetUnavailable, false
	} else if errors.Is(err, state.ErrWorkflowEventTargetUnavailable) {
		code = state.EventFanoutFailureCodeTargetUnavailable
	} else if errors.Is(err, state.ErrWorkflowEventDefinitionInvalid) {
		code, retryable = state.EventFanoutFailureCodeInvalidSubscription, false
	}
	return !errors.Is(err, state.ErrWorkflowEventDefinitionInvalid), &eventFanoutRouteError{
		code: code, retryable: retryable, err: fmt.Errorf("workflow event recipient %s: %w", recipient.ID, err)}
}
