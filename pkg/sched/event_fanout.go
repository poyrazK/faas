package sched

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
	pkgtrace "github.com/onebox-faas/faas/pkg/trace"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

const eventInvocationMethod = "POST"
const eventInvocationPath = "/"
const eventFanoutRecoveryBatch = 100
const eventFanoutSubscriptionBatch = 256
const eventFanoutRecipientMaxAttempts = 12

type eventFanoutRouteError struct {
	code      string
	retryable bool
	err       error
}

func (e *eventFanoutRouteError) Error() string { return e.err.Error() }
func (e *eventFanoutRouteError) Unwrap() error { return e.err }

func eventFanoutFailureDetails(err error) (string, bool) {
	var admissionErr *state.EventRecipientAdmissionError
	if errors.As(err, &admissionErr) {
		return admissionErr.FailureCode, admissionErr.Retryable
	}
	var routeErr *eventFanoutRouteError
	if errors.As(err, &routeErr) {
		return routeErr.code, routeErr.retryable
	}
	return state.EventFanoutFailureCodeInternal, false
}

func eventFanoutRetryable(err error) bool {
	_, retryable := eventFanoutFailureDetails(err)
	return retryable
}

// routePublishedEvent is the schedd-side fanout seam for the internal event
// fabric. The publish endpoint persists the canonical envelope before sending
// its advisory wake; this worker matches only subscriptions owned by the same
// account and turns each match into an ordinary async invocation. The
// invocation ID is deterministic, so reconnects or duplicate LISTEN delivery
// cannot enqueue a second invocation for the same event/subscription pair.
func (l *Loop) routePublishedEvent(ctx context.Context, payload string) error {
	return l.routePublishedEventAt(ctx, payload, time.Time{})
}

func (l *Loop) routePublishedEventAt(ctx context.Context, payload string, acceptedAt time.Time) error {
	if l == nil || l.engine == nil || l.engine.store == nil {
		return nil
	}
	var envelope events.Envelope
	if err := json.Unmarshal([]byte(payload), &envelope); err != nil {
		return fmt.Errorf("sched: decode event.published payload: %w", err)
	}
	if err := envelope.Validate(); err != nil {
		return fmt.Errorf("sched: validate event.published payload: %w", err)
	}
	store, ok := l.engine.store.(state.EventSubscriptionMatcherStore)
	if !ok {
		// Older test stores and mixed-version boxes have no bounded candidate
		// reader yet. The persisted event remains available for a later recovery
		// sweep, so this notification is intentionally a no-op.
		return nil
	}
	eventPayload, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("sched: encode event invocation payload: %w", err)
	}
	now := time.Now().UTC()
	var routeErrs []error
	cursor := state.EventSubscriptionCursor{}
	for {
		subscriptions, listErr := store.ListMatchingEventSubscriptionsForAccount(ctx, envelope.AccountID, envelope.Source, envelope.Type, cursor, eventFanoutSubscriptionBatch)
		if listErr != nil {
			return fmt.Errorf("sched: list matching event subscriptions: %w", listErr)
		}
		for _, row := range subscriptions {
			if !acceptedAt.IsZero() && row.CreatedAt.After(acceptedAt) {
				// Candidate pages are ordered by creation time; later rows
				// cannot have subscribed before this event either.
				return errors.Join(routeErrs...)
			}
			if _, err := l.routeSubscription(ctx, envelope, eventPayload, state.PublishedEventRecipient{
				ID: row.ID, AccountID: row.AccountID, AppID: row.AppID,
				Source: row.Source, Type: row.Type, Filter: row.Filter,
			}, now, false); err != nil {
				routeErrs = append(routeErrs, err)
			}
		}
		if len(subscriptions) < eventFanoutSubscriptionBatch {
			break
		}
		last := subscriptions[len(subscriptions)-1]
		cursor = state.EventSubscriptionCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return errors.Join(routeErrs...)
}

func (l *Loop) routePublishedEventSnapshot(ctx context.Context, work *state.PublishedEventWork) error {
	progressStore, ok := l.engine.store.(state.PublishedEventRecipientProgressStore)
	if !ok {
		return errors.New("sched: event recipient progress store is unavailable")
	}
	var envelope events.Envelope
	if err := json.Unmarshal(work.Payload, &envelope); err != nil {
		return fmt.Errorf("sched: decode event.published payload: %w", err)
	}
	if err := envelope.Validate(); err != nil {
		return fmt.Errorf("sched: validate event.published payload: %w", err)
	}
	if work.RecipientProgress == nil {
		work.RecipientProgress = make(map[string]state.PublishedEventRecipientProgress)
	}
	eventPayload, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("sched: encode event invocation payload: %w", err)
	}
	now := time.Now().UTC()
	if l.now != nil {
		now = l.now().UTC()
	}
	var routeErrs []error
	for _, recipient := range work.RecipientSnapshot {
		previous := work.RecipientProgress[recipient.ID]
		if previous.State == state.PublishedEventRecipientFiltered ||
			previous.State == state.PublishedEventRecipientEnqueued ||
			previous.State == state.PublishedEventRecipientFailed {
			continue
		}
		if previous.NextAttemptAt != nil && previous.NextAttemptAt.After(now) {
			routeErrs = append(routeErrs, state.ErrEventDeliveryCapacity)
			continue
		}
		if len(recipient.Workflow) != 0 && !l.workflowsDispatched {
			// Keep workflow recipients pending while the preview runtime is
			// disabled. Ordinary subscriptions can finish independently.
			routeErrs = append(routeErrs, errors.New("workflow event runtime is disabled"))
			continue
		}
		var matched bool
		var routeErr error
		if len(recipient.Workflow) != 0 {
			matched, routeErr = l.routeWorkflowEvent(ctx, work, envelope, recipient)
		} else if admission, ok := l.engine.store.(state.PublishedEventRecipientAdmissionStore); ok && recipient.ObjectNotification == nil {
			result, err := l.admitEventRecipient(ctx, admission, state.PublishedEventRoutingClaim{
				OutboxID: work.ID, SubscriptionID: recipient.ID, ClaimToken: work.ClaimToken,
			})
			if err == nil {
				work.RecipientProgress[recipient.ID] = result.Progress
				work.Delivered = result.ReceiptSettled
				if result.Progress.State == state.PublishedEventRecipientPending {
					routeErrs = append(routeErrs, state.ErrEventDeliveryCapacity)
				}
				continue
			}
			matched, routeErr = result.Matched, err
		} else {
			matched, routeErr = l.routeSubscription(ctx, envelope, eventPayload, recipient, now, true)
		}
		outcome := state.PublishedEventRecipientProgress{Attempts: previous.Attempts + 1, CapacityDeferrals: previous.CapacityDeferrals, UpdatedAt: now}
		switch {
		case routeErr == nil && matched:
			outcome.State = state.PublishedEventRecipientEnqueued
		case routeErr == nil:
			outcome.State = state.PublishedEventRecipientFiltered
		case !matched && !eventFanoutRetryable(routeErr) || errors.Is(routeErr, state.ErrNotFound):
			outcome.State = state.PublishedEventRecipientFailed
			outcome.FailureCode, outcome.Retryable = eventFanoutFailureDetails(routeErr)
			outcome.LastError = routeErr.Error()
		case outcome.Attempts-outcome.CapacityDeferrals >= eventFanoutRecipientMaxAttempts:
			outcome.State = state.PublishedEventRecipientFailed
			outcome.FailureCode, outcome.Retryable = eventFanoutFailureDetails(routeErr)
			outcome.LastError = routeErr.Error()
		default:
			outcome.State = state.PublishedEventRecipientPending
			outcome.LastError = routeErr.Error()
			routeErrs = append(routeErrs, fmt.Errorf("subscription %s: %w", recipient.ID, routeErr))
		}
		if len(outcome.LastError) > 1024 {
			outcome.LastError = outcome.LastError[:1024]
		}
		if err := progressStore.RecordPublishedEventRecipientProgress(ctx, work.ID, work.ClaimToken, recipient.ID, outcome); err != nil {
			return fmt.Errorf("sched: record event recipient %s progress: %w", recipient.ID, err)
		}
		work.RecipientProgress[recipient.ID] = outcome
		if outcome.State == state.PublishedEventRecipientFailed && l.log != nil {
			l.log.Error("sched: event recipient fanout permanently failed", "event_id", envelope.ID,
				"source", envelope.Source, "subscription_id", recipient.ID, "app_id", recipient.AppID,
				"attempts", outcome.Attempts, "err", outcome.LastError)
		}
	}
	err = errors.Join(routeErrs...)
	if eventFanoutCapacityWait(err) {
		return state.ErrEventDeliveryCapacity
	}
	return err
}

func (l *Loop) routeSubscription(ctx context.Context, envelope events.Envelope, eventPayload []byte, row state.PublishedEventRecipient, now time.Time, requireActiveApp bool) (bool, error) {
	if row.ObjectNotification != nil {
		return l.routeObjectNotification(ctx, envelope, row, now)
	}
	matched, err := (events.Subscription{ID: row.ID, AccountID: row.AccountID, Source: row.Source,
		Type: row.Type, Filter: row.Filter}).Match(envelope)
	if err != nil {
		return false, fmt.Errorf("subscription %s: %w", row.ID, &eventFanoutRouteError{
			code: state.EventFanoutFailureCodeInvalidSubscription, err: err,
		})
	}
	if !matched {
		return false, nil
	}
	if requireActiveApp {
		app, appErr := l.engine.store.AppByID(ctx, row.AppID)
		if errors.Is(appErr, state.ErrNotFound) {
			return true, fmt.Errorf("subscription %s target app is missing: %w", row.ID, &eventFanoutRouteError{
				code: state.EventFanoutFailureCodeTargetUnavailable, err: state.ErrNotFound,
			})
		}
		if appErr != nil {
			return true, fmt.Errorf("subscription %s target app lookup: %w", row.ID, &eventFanoutRouteError{
				code: state.EventFanoutFailureCodeTargetLookupFailed, retryable: true, err: appErr,
			})
		}
		if app.Status == state.AppDeleted {
			return true, fmt.Errorf("subscription %s target app is deleted: %w", row.ID, &eventFanoutRouteError{
				code: state.EventFanoutFailureCodeTargetUnavailable, err: state.ErrNotFound,
			})
		}
		envelopeAccountID, eventAccountErr := uuid.Parse(envelope.AccountID)
		appAccountID, appAccountErr := uuid.Parse(app.AccountID)
		if eventAccountErr != nil || appAccountErr != nil || envelopeAccountID != appAccountID {
			return true, fmt.Errorf("subscription %s target app account mismatch: %w", row.ID, &eventFanoutRouteError{
				code: state.EventFanoutFailureCodeTargetUnavailable, err: state.ErrNotFound,
			})
		}
	}
	invocationID := state.PublishedEventInvocationID(envelope.AccountID, envelope.Source, envelope.ID, row.ID)
	if !requireActiveApp || !row.WorkSnapshotCaptured {
		if cancellations, ok := l.engine.store.(state.WorkCancellationStore); ok {
			if _, lookupErr := cancellations.WorkCancellationByID(ctx, invocationID); lookupErr == nil {
				return true, nil
			} else if !errors.Is(lookupErr, state.ErrNotFound) {
				return true, eventWorkRouteError(row.ID, "look up prior cancellation", lookupErr, true)
			}
		}
	}
	producerHeaders := map[string]string{
		"traceparent": envelope.Traceparent,
		"tracestate":  envelope.Tracestate,
		"baggage":     envelope.Baggage,
	}
	headers, err := json.Marshal(pkgtrace.MergeHeaderMap(
		pkgtrace.ExtractHeaders(ctx, producerHeaders),
		map[string]string{
			"x-gregale-event-id":              envelope.ID,
			"x-gregale-event-source":          envelope.Source,
			"x-gregale-event-type":            envelope.Type,
			"x-gregale-event-subscription-id": row.ID,
		},
	))
	if err != nil {
		return true, fmt.Errorf("subscription %s: encode invocation headers: %w", row.ID, &eventFanoutRouteError{
			code: state.EventFanoutFailureCodeInternal, err: err,
		})
	}
	invocation := state.Invocation{
		ID: invocationID, AppID: row.AppID, AccountID: row.AccountID,
		Source: state.InvocationAsyncInvoke, State: state.InvocationPending,
		Method: eventInvocationMethod, Path: eventInvocationPath,
		Payload: eventPayload, Headers: headers, DueAt: now, CreatedAt: now,
	}
	if handled, routeErr := l.routeBoundEventWork(ctx, row, invocation, requireActiveApp && row.WorkSnapshotCaptured); handled || routeErr != nil {
		return true, routeErr
	}
	_, err = l.engine.store.EnqueueInvocation(ctx, invocation)
	if err != nil && !errors.Is(err, state.ErrConflict) {
		return true, fmt.Errorf("subscription %s: enqueue invocation: %w", row.ID, &eventFanoutRouteError{
			code: state.EventFanoutFailureCodeInvocationEnqueueFailed, retryable: true, err: err,
		})
	}
	if err == nil && l.pool != nil {
		_ = db.Notify(ctx, l.pool, db.NotifyInvocationDue,
			fmt.Sprintf(`{"invocation_id":"%s","app_id":"%s","source":"%s"}`, invocationID, row.AppID, state.InvocationAsyncInvoke))
	}
	return true, nil
}

func eventWorkRouteError(subscriptionID, operation string, err error, retryable bool) error {
	code := state.EventFanoutFailureCodeInvalidSubscription
	if retryable {
		code = state.EventFanoutFailureCodeInvocationEnqueueFailed
	}
	return fmt.Errorf("subscription %s: %s: %w", subscriptionID, operation,
		&eventFanoutRouteError{code: code, retryable: retryable, err: err})
}

// routeBoundEventWork applies the same app-scoped policy used by explicit
// invocations. A receipt or existing invocation wins over changed selectors
// when an event is replayed.
func (l *Loop) routeBoundEventWork(ctx context.Context, row state.PublishedEventRecipient, invocation state.Invocation, useSnapshot bool) (bool, error) {
	var binding state.PublishedEventWorkBindingSnapshot
	if useSnapshot {
		if row.Work == nil {
			return false, nil
		}
		binding = *row.Work
	} else {
		bindings, ok := l.engine.store.(state.EventWorkBindingStore)
		if !ok {
			return false, nil
		}
		byID, err := bindings.EventWorkBindingsByIDs(ctx, []string{row.ID})
		if err != nil {
			return true, eventWorkRouteError(row.ID, "load work binding", err, true)
		}
		live, ok := byID[row.ID]
		if !ok {
			return false, nil
		}
		binding = state.PublishedEventWorkBindingSnapshot{
			PolicyName: live.PolicyName, KeySelector: live.KeySelector,
			FairnessSelector: live.FairnessSelector, Action: live.Action,
		}
	}
	if _, err := l.engine.store.InvocationByID(ctx, invocation.ID); err == nil {
		return true, nil
	} else if !errors.Is(err, state.ErrNotFound) {
		return true, eventWorkRouteError(row.ID, "look up prior delivery", err, true)
	}
	selector, err := workpolicy.ParseSelector(binding.KeySelector)
	if err != nil {
		return true, eventWorkRouteError(row.ID, "parse work key selector", err, false)
	}
	key, err := selector.Resolve(invocation.Payload)
	if err != nil {
		return true, eventWorkRouteError(row.ID, "resolve work key", err, false)
	}
	if binding.Action == state.EventWorkCancelPending {
		cancellations, ok := l.engine.store.(state.WorkCancellationStore)
		if !ok {
			return true, eventWorkRouteError(row.ID, "work cancellation store unavailable", errors.New("store capability unavailable"), true)
		}
		_, err := cancellations.CancelPendingKeyedInvocations(ctx, row.AppID, binding.PolicyName, key, invocation.ID)
		if err != nil {
			return true, eventWorkRouteError(row.ID, "cancel pending work", err, true)
		}
		return true, nil
	}
	var policy workpolicy.Policy
	if useSnapshot && binding.Policy != nil {
		policy, err = binding.Policy.EffectivePolicy(binding.PolicyName)
		if err != nil {
			return true, eventWorkRouteError(row.ID, "decode accepted work policy", err, false)
		}
		invocation.WorkPolicyRevision = binding.Policy.Revision
	} else {
		// Receipts accepted before policy snapshots still resolve the live
		// policy, matching their original routing contract.
		policies, ok := l.engine.store.(state.AppWorkPolicyStore)
		if !ok {
			return true, eventWorkRouteError(row.ID, "work policy store unavailable", errors.New("store capability unavailable"), true)
		}
		record, lookupErr := policies.AppWorkPolicyByName(ctx, row.AppID, binding.PolicyName)
		if lookupErr != nil {
			return true, eventWorkRouteError(row.ID, "load work policy", lookupErr, !errors.Is(lookupErr, state.ErrNotFound))
		}
		policy = record.Policy
		invocation.WorkPolicyRevision = record.Revision
	}
	var fairness []string
	if binding.FairnessSelector != "" {
		selector, parseErr := workpolicy.ParseSelector(binding.FairnessSelector)
		if parseErr != nil {
			return true, eventWorkRouteError(row.ID, "parse fairness selector", parseErr, false)
		}
		fairnessKey, resolveErr := selector.Resolve(invocation.Payload)
		if resolveErr != nil {
			return true, eventWorkRouteError(row.ID, "resolve fairness key", resolveErr, false)
		}
		fairness = append(fairness, fairnessKey)
	}
	if _, err := l.engine.store.EnqueueKeyedInvocation(ctx, invocation, policy, key, fairness...); err != nil {
		if errors.Is(err, state.ErrConflict) {
			return true, nil
		}
		return true, eventWorkRouteError(row.ID, "enqueue keyed invocation", err, true)
	}
	if l.pool != nil {
		_ = db.Notify(ctx, l.pool, db.NotifyInvocationDue,
			fmt.Sprintf(`{"invocation_id":"%s","app_id":"%s","source":"%s"}`, invocation.ID, row.AppID, state.InvocationAsyncInvoke))
	}
	return true, nil
}

// runEventFanoutSweep drains durable claims in bounded batches. The outbox is
// populated in the same transaction as each event.published ledger row, so a
// restart or an arbitrarily long LISTEN gap cannot strand accepted events.
func (l *Loop) runEventFanoutSweep(ctx context.Context) {
	if l == nil || l.engine == nil || l.engine.store == nil {
		return
	}
	store, ok := l.engine.store.(state.PublishedEventWorkStore)
	if !ok {
		return
	}
	// Disabling adoption must still drain previously adopted receipts.
	defer l.observeEventRoutingHealth(ctx)
	defer l.runEventRecipientSweep(ctx)
	now := time.Now().UTC()
	if l.now != nil {
		now = l.now().UTC()
	}
	l.runEventHistoryPrune(ctx, now)
	if now.Sub(l.eventFanoutLastPrune) >= 10*time.Second {
		if retention, ok := l.engine.store.(state.PublishedEventRetentionStore); ok {
			if _, err := retention.PruneDeliveredPublishedEvents(ctx, now.Add(-state.PublishedEventIdentityRetention), 5000); err != nil {
				if l.log != nil {
					l.log.Warn("sched: prune delivered event identities failed", "err", err)
				}
			} else {
				if backfills, ok := l.engine.store.(state.EventReplayBackfillStore); ok {
					if _, err := backfills.PruneEventReplayBackfills(ctx, now, api.EventReplayBackfillPruneBatch); err != nil && l.log != nil {
						l.log.Warn("sched: prune completed event replay backfills failed", "err", err)
					}
				}
				l.eventFanoutLastPrune = now
			}
		}
	}
	l.runEventReplayBackfillSweep(ctx, now)
	for i := 0; i < eventFanoutRecoveryBatch; i++ {
		now := time.Now().UTC()
		if l.now != nil {
			now = l.now().UTC()
		}
		work, err := store.ClaimDuePublishedEvent(ctx, now)
		if errors.Is(err, state.ErrNotFound) {
			return
		}
		if err != nil {
			if l.log != nil {
				l.log.Warn("sched: claim event fanout failed", "err", err)
			}
			return
		}
		var routeErr error
		if recipients, ok := l.engine.store.(state.PublishedEventRecipientWorkStore); ok && l.eventRecipientClaims && work.SnapshotCaptured && l.eventRecipientAdoptionSupported(work) {
			routeErr = recipients.InitializePublishedEventRecipients(ctx, work, now)
			if routeErr == nil {
				continue
			}
		} else if work.SnapshotCaptured {
			routeErr = l.routePublishedEventSnapshot(ctx, work)
		} else if _, ok := l.engine.store.(state.EventSubscriptionMatcherStore); ok {
			// Receipts accepted before the snapshot migration retain their
			// existing current-subscription routing behavior.
			routeErr = l.routePublishedEventAt(ctx, string(work.Payload), work.CreatedAt)
		} else {
			routeErr = errors.New("sched: legacy event receipt requires subscription matcher")
		}
		if routeErr != nil && !eventFanoutCapacityWait(routeErr) && l.log != nil {
			l.log.Warn("sched: event fanout failed", "outbox_id", work.ID, "err", routeErr)
		}
		if work.Delivered {
			continue
		}
		if err := store.FinishPublishedEvent(ctx, work.ID, work.ClaimToken, routeErr); err != nil && l.log != nil {
			l.log.Warn("sched: finish event fanout failed", "outbox_id", work.ID, "err", err)
		}
	}
}

func (l *Loop) runEventReplayBackfillSweep(ctx context.Context, now time.Time) {
	store, ok := l.engine.store.(state.EventReplayBackfillStore)
	if !ok {
		return
	}
	for i := 0; i < eventFanoutRecoveryBatch; i++ {
		worked, err := store.ProcessNextEventReplayBackfill(ctx, now)
		if err != nil {
			if l.log != nil {
				l.log.Warn("sched: process event replay backfill page failed", "err", err)
			}
			return
		}
		if !worked {
			return
		}
	}
}

// WithEventRecipientClaims overrides the default independent recipient adoption.
// Disable adoption during mixed-version upgrades; adopted work always drains.
func (l *Loop) WithEventRecipientClaims(enabled bool) *Loop {
	l.eventRecipientClaims = enabled
	return l
}

func (l *Loop) runEventRecipientSweep(ctx context.Context) {
	store, ok := l.engine.store.(state.PublishedEventRecipientWorkStore)
	if !ok {
		return
	}
	for i := 0; i < eventFanoutRecoveryBatch; i++ {
		now := time.Now().UTC()
		if l.now != nil {
			now = l.now().UTC()
		}
		work, err := store.ClaimDuePublishedEventRecipient(ctx, now, l.workflowsDispatched)
		if errors.Is(err, state.ErrNotFound) {
			return
		}
		if err != nil {
			if l.log != nil {
				l.log.Error("sched: claim event recipient failed", "err", err)
			}
			return
		}
		var envelope events.Envelope
		routeErr := json.Unmarshal(work.Payload, &envelope)
		if routeErr == nil {
			routeErr = envelope.Validate()
		}
		var eventPayload []byte
		if routeErr == nil {
			eventPayload, routeErr = json.Marshal(envelope)
		}
		matched := false
		if routeErr == nil {
			if len(work.Recipient.Workflow) != 0 {
				admission, ok := l.engine.store.(state.EventWorkflowRecipientAdmissionStore)
				if !ok {
					routeErr = &eventFanoutRouteError{code: state.EventFanoutFailureCodeInternal, retryable: true,
						err: errors.New("workflow recipient admission store is unavailable")}
				} else {
					result, err := admission.AdmitEventWorkflowRecipient(ctx, state.PublishedEventRoutingClaim{
						OutboxID: work.OutboxID, SubscriptionID: work.Recipient.ID,
						ClaimToken: work.ClaimToken, Generation: work.Generation,
					})
					if err == nil {
						continue
					}
					matched, routeErr = result.Matched, err
				}
			} else if admission, ok := l.engine.store.(state.PublishedEventRecipientAdmissionStore); ok && work.Recipient.ObjectNotification == nil {
				result, err := l.admitEventRecipient(ctx, admission, state.PublishedEventRoutingClaim{
					OutboxID: work.OutboxID, SubscriptionID: work.Recipient.ID,
					ClaimToken: work.ClaimToken, Generation: work.Generation, BackfillJobID: work.BackfillJobID,
				})
				if err == nil {
					continue
				}
				matched, routeErr = result.Matched, err
			} else {
				matched, routeErr = l.routeSubscription(ctx, envelope, eventPayload, work.Recipient, now, true)
			}
		}
		finishedAt := time.Now().UTC()
		if l.now != nil {
			finishedAt = l.now().UTC()
		}
		progress := state.PublishedEventRecipientProgress{Attempts: work.TotalAttempts, CapacityDeferrals: work.CapacityDeferrals, UpdatedAt: finishedAt}
		switch {
		case routeErr == nil && matched:
			progress.State = state.PublishedEventRecipientEnqueued
		case routeErr == nil:
			progress.State = state.PublishedEventRecipientFiltered
		case !matched && !eventFanoutRetryable(routeErr) || errors.Is(routeErr, state.ErrNotFound) || work.Attempts-work.GenerationCapacityDeferrals >= eventFanoutRecipientMaxAttempts:
			progress.State = state.PublishedEventRecipientFailed
			progress.FailureCode, progress.Retryable = eventFanoutFailureDetails(routeErr)
			progress.LastError = routeErr.Error()
		default:
			progress.State = state.PublishedEventRecipientPending
			progress.LastError = routeErr.Error()
		}
		if len(progress.LastError) > 1024 {
			progress.LastError = progress.LastError[:1024]
		}
		backoff := 5 * time.Second << min(max(0, work.Attempts-work.GenerationCapacityDeferrals-1), 6)
		next := finishedAt.Add(min(backoff, 300*time.Second))
		if err := store.FinishPublishedEventRecipient(ctx, work, progress, next); err != nil && l.log != nil {
			l.log.Error("sched: finish event recipient failed", "outbox_id", work.OutboxID,
				"subscription_id", work.Recipient.ID, "err", err)
		} else if progress.State == state.PublishedEventRecipientFailed && l.log != nil {
			l.log.Error("sched: event recipient routing failed", "outbox_id", work.OutboxID,
				"subscription_id", work.Recipient.ID, "failure_code", progress.FailureCode,
				"retryable", progress.Retryable, "err", progress.LastError)
		}
	}
}

// Advisory wakes follow the commit. The invocation drain also polls, so a
// lost notification or an unknown commit response cannot lose admitted work.
func (l *Loop) admitEventRecipient(ctx context.Context, store state.PublishedEventRecipientAdmissionStore, claim state.PublishedEventRoutingClaim) (state.PublishedEventRoutingResult, error) {
	result, err := store.AdmitPublishedEventRecipient(ctx, claim)
	if err == nil && result.CapacityDeferred {
		l.ops.ObserveEventDeliveryCapacityDeferral(result.Progress.CapacityScope)
	}
	if err == nil && result.InvocationCreated && l.pool != nil {
		_ = db.Notify(ctx, l.pool, db.NotifyInvocationDue,
			fmt.Sprintf(`{"invocation_id":"%s","app_id":"%s","source":"%s"}`, result.DeliveryID, result.AppID, state.InvocationAsyncInvoke))
	}
	return result, err
}

func (l *Loop) observeEventRoutingHealth(ctx context.Context) {
	if l.ops == nil {
		return
	}
	if store, ok := l.engine.store.(state.EventRoutingHealthStore); ok {
		h, err := store.EventRoutingHealth(ctx)
		if err == nil {
			l.ops.SetEventRoutingHealth(h.CapacityWaiting, h.OldestPendingSeconds)
		}
	}
}

// Inspect every joined error so a real failure alongside a capacity wait still
// produces a failure warning. Single wrappers preserve the same classification.
func eventFanoutCapacityWait(err error) bool {
	if err == nil {
		return false
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		children := joined.Unwrap()
		for _, child := range children {
			if !eventFanoutCapacityWait(child) {
				return false
			}
		}
		return len(children) > 0
	}
	if child := errors.Unwrap(err); child != nil {
		return eventFanoutCapacityWait(child)
	}
	return errors.Is(err, state.ErrEventDeliveryCapacity)
}

// Detail retention is independent of receipt settlement. Parent SKIP LOCKED
// locks make this maintenance yield to active routing and operator replay.
func (l *Loop) runEventHistoryPrune(ctx context.Context, now time.Time) {
	if now.Sub(l.eventFanoutHistoryLastPrune) < 10*time.Second {
		return
	}
	retention, ok := l.engine.store.(state.EventFanoutHistoryRetentionStore)
	if !ok {
		return
	}
	if _, err := retention.PruneEventFanoutHistory(ctx, now, api.EventRoutingHistoryPruneBatch); err != nil {
		if l.log != nil {
			l.log.Warn("sched: compact routing history failed", "err", err)
		}
		return
	}
	l.eventFanoutHistoryLastPrune = now
}

func (l *Loop) eventRecipientAdoptionSupported(work *state.PublishedEventWork) bool {
	for _, recipient := range work.RecipientSnapshot {
		if len(recipient.Workflow) != 0 {
			_, supported := l.engine.store.(state.EventWorkflowRecipientAdmissionStore)
			return supported
		}
	}
	return true
}
