package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

// ErrEventDeliveryCapacity is a durable wait, not a routing failure.
var ErrEventDeliveryCapacity = errors.New("state: event delivery capacity exhausted")

type EventDeliveryCapacityError struct{ Scope string }

func (e *EventDeliveryCapacityError) Error() string {
	return ErrEventDeliveryCapacity.Error() + ": " + e.Scope
}
func (e *EventDeliveryCapacityError) Unwrap() error { return ErrEventDeliveryCapacity }

// EventDeliveryCapacityRetryDelay bounds pressure polling without spending the
// twelve-failure routing budget. Execution completion releases capacity.
const EventDeliveryCapacityRetryDelay = 5 * time.Second

type eventDeliverySlot struct{ AccountID, AppID, SubscriptionID string }

func eventCapacityScope(l api.EventDeliveryLimits, consumer, app, account int64) string {
	switch {
	case consumer >= int64(l.PerConsumer):
		return "consumer"
	case app >= int64(l.PerApp):
		return "app"
	case account >= int64(l.PerAccount):
		return "account"
	}
	return ""
}

func eventAdmissionNeedsCapacity(p eventAdmissionPlan) bool {
	return p.matched && !p.prior && !p.cancel
}
func eventAdmissionReplacesPending(p eventAdmissionPlan) bool {
	return p.invocation.WorkPolicyName != "" && p.policy.PendingUpdates == workpolicy.PendingKeepLatest
}

func lockEventCapacity(ctx context.Context, tx sqlc.DBTX, accountID string, limits api.EventDeliveryLimits) error {
	return sqlc.New().EventDeliveryLockCapacity(ctx, tx, sqlc.EventDeliveryLockCapacityParams{
		AccountID: mustPgUUID(accountID), ConsumerLimit: int32(limits.PerConsumer), AppLimit: int32(limits.PerApp), AccountLimit: int32(limits.PerAccount)})
}

func eventCapacityTx(ctx context.Context, tx sqlc.DBTX, p eventAdmissionPlan, limits api.EventDeliveryLimits) (string, error) {
	counts, err := sqlc.New().EventDeliveryCounts(ctx, tx, sqlc.EventDeliveryCountsParams{
		AccountID: mustPgUUID(p.recipient.AccountID), AppID: mustPgUUID(p.recipient.AppID), SubscriptionID: p.recipient.ID,
		ReplacePending: eventAdmissionReplacesPending(p), PolicyName: p.invocation.WorkPolicyName, KeyDigest: p.invocation.WorkKeyDigest})
	if err != nil {
		return "", err
	}
	return eventCapacityScope(limits, counts.ConsumerCount, counts.AppCount, counts.AccountCount), nil
}

func (m *MemStore) eventCapacityLocked(slot eventDeliverySlot, replacing *eventAdmissionPlan) string {
	acct, ok := m.accounts[slot.AccountID]
	if !ok {
		acct, ok = m.accounts[canonicalMemUUID(slot.AccountID)]
	}
	if !ok {
		acct = m.accounts[strings.ReplaceAll(canonicalMemUUID(slot.AccountID), "-", "")]
	}
	planLimits, ok := api.LimitsFor(acct.Plan)
	if !ok {
		planLimits = api.MustLimitsFor(api.PlanFree)
	}
	limits := planLimits.EventDeliveries
	var consumer, app, account int64
	for id, s := range m.eventDeliverySlots {
		inv, ok := m.invocations[id]
		if !ok || !sameMemUUID(s.AccountID, slot.AccountID) || (inv.State != InvocationPending && inv.State != InvocationDispatching) {
			continue
		}
		if replacing != nil && eventAdmissionReplacesPending(*replacing) && inv.State == InvocationPending &&
			sameMemUUID(inv.AppID, replacing.invocation.AppID) && inv.WorkPolicyName == replacing.invocation.WorkPolicyName && bytes.Equal(inv.WorkKeyDigest, replacing.invocation.WorkKeyDigest) {
			continue
		}
		account++
		if sameMemUUID(s.AppID, slot.AppID) {
			app++
			if s.SubscriptionID == slot.SubscriptionID {
				consumer++
			}
		}
	}
	return eventCapacityScope(limits, consumer, app, account)
}

func (m *MemStore) recordEventDeliverySlotLocked(id string, slot eventDeliverySlot) {
	if m.eventDeliverySlots == nil {
		m.eventDeliverySlots = make(map[string]eventDeliverySlot)
	}
	m.eventDeliverySlots[id] = slot
}

// Called before inserting a replay child or restoring an in-place DLQ entry.
// Callers record the inherited slot only after all validation succeeds.
func (m *MemStore) eventReplayCapacityLocked(parentID string) (eventDeliverySlot, error) {
	slot, ok := m.eventDeliverySlots[parentID]
	if !ok {
		return slot, nil
	}
	if scope := m.eventCapacityLocked(slot, nil); scope != "" {
		return slot, &EventDeliveryCapacityError{Scope: scope}
	}
	return slot, nil
}

func refreshEventReplayCapacity(ctx context.Context, tx sqlc.DBTX, id string) error {
	row, err := sqlc.New().EventDeliveryReplayAccount(ctx, tx, mustPgUUID(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	limits, ok := api.LimitsFor(api.Plan(row.Plan))
	if !ok {
		return ErrInvalidArgument
	}
	return lockEventCapacity(ctx, tx, uuidString(row.AccountID), limits.EventDeliveries)
}

func eventCapacityProgress(previous PublishedEventRecipientProgress, attempts int, scope string) PublishedEventRecipientProgress {
	now := time.Now().UTC()
	next := now.Add(EventDeliveryCapacityRetryDelay)
	return PublishedEventRecipientProgress{DeliveryAgeOverride: previous.DeliveryAgeOverride, State: PublishedEventRecipientPending, Attempts: attempts,
		RetrySpentMS: previous.RetrySpentMS, RetryGeneration: previous.RetryGeneration, CapacityDeferrals: previous.CapacityDeferrals + 1, CapacityScope: scope, NextAttemptAt: &next, UpdatedAt: now}
}

func preserveEventCapacityHistory(progress *PublishedEventRecipientProgress, previous PublishedEventRecipientProgress) {
	progress.DeliveryAgeOverride = previous.DeliveryAgeOverride
	progress.CapacityDeferrals = previous.CapacityDeferrals
	progress.RetrySpentMS = previous.RetrySpentMS
	progress.RetryGeneration = previous.RetryGeneration
}

func eventRoutingAccount(receipt *PublishedEventWork) string {
	var envelope struct {
		AccountID string `json:"accountid"`
		Legacy    string `json:"account_id"`
	}
	_ = json.Unmarshal(receipt.Payload, &envelope)
	if envelope.AccountID == "" {
		envelope.AccountID = envelope.Legacy
	}
	return canonicalMemUUID(envelope.AccountID)
}
func (m *MemStore) eventFairTimeLocked(account, consumer string) time.Time {
	return m.eventRoutingFairness[account+"/"+consumer]
}
func (m *MemStore) recordEventFairClaimLocked(account, consumer string) {
	if m.eventRoutingFairness == nil {
		m.eventRoutingFairness = make(map[string]time.Time)
	}
	now := time.Now().UTC()
	m.eventRoutingFairness[account+"/"] = now
	if consumer != "" {
		m.eventRoutingFairness[account+"/"+consumer] = now
	}
}

// Health uses bounded labels in the scheduler and immutable acceptance time.
type EventRoutingHealth struct {
	CapacityWaiting      int64
	OldestPendingSeconds float64
}
type EventRoutingHealthStore interface {
	EventRoutingHealth(context.Context) (EventRoutingHealth, error)
}

func (s *PgStore) EventRoutingHealth(ctx context.Context) (EventRoutingHealth, error) {
	r, err := sqlc.New().EventRoutingHealth(ctx, s.pool)
	return EventRoutingHealth{CapacityWaiting: r.CapacityWaiting, OldestPendingSeconds: r.OldestPendingSeconds}, err
}
func (m *MemStore) EventRoutingHealth(_ context.Context) (EventRoutingHealth, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var h EventRoutingHealth
	var oldest time.Time
	for _, r := range m.eventFanout {
		if r.Delivered {
			continue
		}
		for _, sub := range r.RecipientSnapshot {
			p := r.RecipientProgress[sub.ID]
			if p.State == PublishedEventRecipientEnqueued || p.State == PublishedEventRecipientFiltered || p.State == PublishedEventRecipientFailed {
				continue
			}
			if p.CapacityScope != "" {
				h.CapacityWaiting++
			}
			if oldest.IsZero() || r.CreatedAt.Before(oldest) {
				oldest = r.CreatedAt
			}
		}
	}
	if !oldest.IsZero() {
		h.OldestPendingSeconds = max(0, time.Since(oldest).Seconds())
	}
	return h, nil
}

func eventProgressNext(p PublishedEventRecipientProgress) time.Time {
	if p.NextAttemptAt != nil {
		return *p.NextAttemptAt
	}
	return p.UpdatedAt
}

func eventAdoptionAvailableAt(p PublishedEventRecipientProgress, now time.Time) time.Time {
	if p.NextAttemptAt != nil && p.NextAttemptAt.After(now) {
		return *p.NextAttemptAt
	}
	return now
}

func eventRecipientDue(p PublishedEventRecipientProgress, now time.Time) bool {
	return !routingAdmissionRecorded(p) && p.State != PublishedEventRecipientFailed && (p.NextAttemptAt == nil || !p.NextAttemptAt.After(now))
}
func (m *MemStore) eventReceiptConsumerFairTimeLocked(receipt *PublishedEventWork, now time.Time) time.Time {
	account := eventRoutingAccount(receipt)
	oldest := m.eventFairTimeLocked(account, "")
	found := false
	for _, r := range receipt.RecipientSnapshot {
		if !eventRecipientDue(receipt.RecipientProgress[r.ID], now) {
			continue
		}
		at := m.eventFairTimeLocked(account, r.ID)
		if !found || at.Before(oldest) {
			oldest = at
			found = true
		}
	}
	return oldest
}
