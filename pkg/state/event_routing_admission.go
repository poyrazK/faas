package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/eventcontract"
	"github.com/onebox-faas/faas/pkg/workpolicy"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// PublishedEventRoutingClaim identifies authority, never caller-supplied work.
// Generation zero denotes a whole-receipt lease; positive generations denote
// independent recipient leases. All routing inputs come from stored snapshots.
type PublishedEventRoutingClaim struct {
	OutboxID       int64
	SubscriptionID string
	ClaimToken     string
	Generation     int64
	BackfillJobID  string
}

type PublishedEventRoutingResult struct {
	Matched           bool
	DeliveryID        string
	AppID             string
	InvocationCreated bool
	CapacityDeferred  bool
	Progress          PublishedEventRecipientProgress
	ReceiptSettled    bool
}

type PublishedEventRecipientAdmissionStore interface {
	AdmitPublishedEventRecipient(context.Context, PublishedEventRoutingClaim) (PublishedEventRoutingResult, error)
}

// EventRecipientAdmissionError preserves routing failure classification for
// scheduler retries and the existing operator receipt/failure projections.
type EventRecipientAdmissionError struct {
	FailureCode string
	Retryable   bool
	Err         error
}

func (e *EventRecipientAdmissionError) Error() string { return e.Err.Error() }
func (e *EventRecipientAdmissionError) Unwrap() error { return e.Err }

func admissionError(code string, retryable bool, err error) error {
	return &EventRecipientAdmissionError{FailureCode: code, Retryable: retryable, Err: err}
}

type eventAdmissionLookup interface {
	InvocationByID(context.Context, string) (Invocation, error)
	WorkCancellationByID(context.Context, string) (WorkCancellation, error)
	EventWorkBindingsByIDs(context.Context, []string) (map[string]EventWorkBinding, error)
	AppWorkPolicyByName(context.Context, string, string) (AppWorkPolicy, error)
}

type eventAdmissionPlan struct {
	recipient  PublishedEventRecipient
	invocation Invocation
	matched    bool
	prior      bool
	policy     workpolicy.Policy
	key        string
	fairness   []string
	cancel     bool
}

func routingRecipient(receipt *PublishedEventWork, claim PublishedEventRoutingClaim) (PublishedEventRecipient, error) {
	if receipt == nil || !receipt.SnapshotCaptured || claim.OutboxID != receipt.ID || claim.Generation < 0 ||
		receipt.RecipientClaims != (claim.Generation > 0) {
		return PublishedEventRecipient{}, ErrConflict
	}
	if _, err := uuid.Parse(claim.ClaimToken); err != nil {
		return PublishedEventRecipient{}, ErrConflict
	}
	if claim.BackfillJobID != "" {
		if recipient, ok := receipt.replayRecipients[claim.SubscriptionID]; ok && recipient.ID == claim.SubscriptionID {
			if recipient.ObjectNotification != nil || len(recipient.Workflow) != 0 {
				return PublishedEventRecipient{}, ErrInvalidArgument
			}
			return recipient, nil
		}
		return PublishedEventRecipient{}, ErrNotFound
	}
	for _, r := range receipt.RecipientSnapshot {
		if r.ID == claim.SubscriptionID {
			if r.ObjectNotification != nil || len(r.Workflow) != 0 {
				return PublishedEventRecipient{}, ErrInvalidArgument
			}
			return r, nil
		}
	}
	return PublishedEventRecipient{}, ErrNotFound
}

func routingAdmissionRecorded(progress PublishedEventRecipientProgress) bool {
	return progress.State == PublishedEventRecipientEnqueued || progress.State == PublishedEventRecipientFiltered
}

func newEventAdmissionPlan(ctx context.Context, receipt *PublishedEventWork, claim PublishedEventRoutingClaim) (eventAdmissionPlan, error) {
	r, err := routingRecipient(receipt, claim)
	if err != nil {
		return eventAdmissionPlan{}, err
	}
	p := eventAdmissionPlan{recipient: r}
	var envelope eventcontract.Envelope
	if err := json.Unmarshal(receipt.Payload, &envelope); err != nil {
		return p, err
	}
	if err := envelope.Validate(); err != nil {
		return p, err
	}
	if !sameMemUUID(envelope.AccountID, r.AccountID) {
		return p, admissionError(EventFanoutFailureCodeTargetUnavailable, false, ErrNotFound)
	}
	p.matched, err = (eventcontract.Subscription{ID: r.ID, AccountID: r.AccountID, Source: r.Source, Type: r.Type, Filter: r.Filter}).Match(envelope)
	if err != nil {
		return p, admissionError(EventFanoutFailureCodeInvalidSubscription, false, err)
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return p, err
	}
	traceContext := otel.GetTextMapPropagator().Extract(ctx, propagation.MapCarrier{
		"traceparent": envelope.Traceparent, "tracestate": envelope.Tracestate, "baggage": envelope.Baggage,
	})
	headerMap := propagation.MapCarrier{"x-gregale-event-id": envelope.ID, "x-gregale-event-source": envelope.Source,
		"x-gregale-event-type": envelope.Type, "x-gregale-event-subscription-id": r.ID}
	otel.GetTextMapPropagator().Inject(traceContext, headerMap)
	if span := trace.SpanContextFromContext(traceContext); span.IsValid() {
		headerMap[api.TraceIDHeader] = span.TraceID().String()
	}
	headers, err := json.Marshal(headerMap)
	if err != nil {
		return p, err
	}
	now := time.Now().UTC()
	p.invocation = Invocation{ID: PublishedEventInvocationID(envelope.AccountID, envelope.Source, envelope.ID, r.ID),
		AppID: r.AppID, AccountID: r.AccountID, Source: InvocationAsyncInvoke, State: InvocationPending,
		Method: "POST", Path: "/", Payload: payload, Headers: headers, CreatedAt: now, DueAt: now}
	return p, nil
}

// A retained historical handoff can be reconciled before resolving live
// policies. The checkpoint thereafter remains authoritative if rows are pruned.
func prepareEventAdmission(ctx context.Context, store eventAdmissionLookup, receipt *PublishedEventWork, p *eventAdmissionPlan) error {
	if !p.matched || routingAdmissionRecorded(receipt.RecipientProgress[p.recipient.ID]) {
		return nil
	}
	inv, err := store.InvocationByID(ctx, p.invocation.ID)
	if err == nil {
		if !sameMemUUID(inv.AppID, p.invocation.AppID) || !sameMemUUID(inv.AccountID, p.invocation.AccountID) || inv.Source != InvocationAsyncInvoke ||
			inv.CreatedAt.Before(receipt.CreatedAt) || !jsonEqual(inv.Payload, p.invocation.Payload) {
			return ErrConflict
		}
		p.prior = true
		return nil
	}
	if !errors.Is(err, ErrNotFound) {
		return err
	}
	cancellation, err := store.WorkCancellationByID(ctx, p.invocation.ID)
	if err == nil {
		if !sameMemUUID(cancellation.AppID, p.invocation.AppID) || cancellation.CreatedAt.Before(receipt.CreatedAt) {
			return ErrConflict
		}
		p.prior = true
		return nil
	}
	if !errors.Is(err, ErrNotFound) {
		return err
	}
	binding := p.recipient.Work
	if !p.recipient.WorkSnapshotCaptured {
		byID, err := store.EventWorkBindingsByIDs(ctx, []string{p.recipient.ID})
		if err != nil {
			return err
		}
		if live, ok := byID[p.recipient.ID]; ok {
			binding = &PublishedEventWorkBindingSnapshot{PolicyName: live.PolicyName, KeySelector: live.KeySelector, FairnessSelector: live.FairnessSelector, Action: live.Action}
		}
	}
	if binding == nil {
		return nil
	}
	selector, err := workpolicy.ParseSelector(binding.KeySelector)
	if err != nil {
		return admissionError(EventFanoutFailureCodeInvalidSubscription, false, err)
	}
	p.key, err = selector.Resolve(p.invocation.Payload)
	if err != nil {
		return admissionError(EventFanoutFailureCodeInvalidSubscription, false, err)
	}
	p.cancel = binding.Action == EventWorkCancelPending
	if p.cancel {
		_, digest, err := validateWorkCancellation(p.invocation.AppID, binding.PolicyName, p.key, p.invocation.ID)
		p.invocation.WorkPolicyName, p.invocation.WorkKeyDigest = binding.PolicyName, digest[:]
		return err
	}
	if p.recipient.WorkSnapshotCaptured && binding.Policy != nil {
		p.policy, err = binding.Policy.EffectivePolicy(binding.PolicyName)
		p.invocation.WorkPolicyRevision = binding.Policy.Revision
	} else {
		record, lookupErr := store.AppWorkPolicyByName(ctx, p.invocation.AppID, binding.PolicyName)
		if lookupErr != nil {
			return admissionError(EventFanoutFailureCodeInvalidSubscription, !errors.Is(lookupErr, ErrNotFound), lookupErr)
		}
		p.policy, p.invocation.WorkPolicyRevision = record.Policy, record.Revision
	}
	if err != nil {
		return admissionError(EventFanoutFailureCodeInvalidSubscription, false, err)
	}
	if binding.FairnessSelector != "" {
		selector, err := workpolicy.ParseSelector(binding.FairnessSelector)
		if err != nil {
			return admissionError(EventFanoutFailureCodeInvalidSubscription, false, err)
		}
		key, err := selector.Resolve(p.invocation.Payload)
		if err != nil {
			return admissionError(EventFanoutFailureCodeInvalidSubscription, false, err)
		}
		p.fairness = []string{key}
	}
	p.invocation, err = prepareKeyedInvocation(p.invocation, p.policy, p.key, p.fairness)
	return err
}

func eventAdmissionResult(p eventAdmissionPlan, progress PublishedEventRecipientProgress, created, settled bool) PublishedEventRoutingResult {
	return PublishedEventRoutingResult{Matched: p.matched, DeliveryID: p.invocation.ID, AppID: p.recipient.AppID,
		InvocationCreated: created, Progress: progress, ReceiptSettled: settled}
}

func eventAdmissionProgress(p eventAdmissionPlan, attempts int) PublishedEventRecipientProgress {
	state := PublishedEventRecipientFiltered
	if p.matched {
		state = PublishedEventRecipientEnqueued
	}
	return PublishedEventRecipientProgress{State: state, Attempts: attempts, UpdatedAt: time.Now().UTC()}
}

func eventAdmissionPrepareError(err error) error {
	var classified *EventRecipientAdmissionError
	if errors.As(err, &classified) || errors.Is(err, ErrConflict) {
		return err
	}
	return admissionError(EventFanoutFailureCodeInvocationEnqueueFailed, true, fmt.Errorf("prepare event admission: %w", err))
}
