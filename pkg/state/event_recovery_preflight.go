package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type EventRecoveryPreflightStore interface {
	GetEventRecoveryPreflight(context.Context, string, string, time.Time) (api.EventRecoveryPreflight, error)
}

func newRecoveryPreflight(job api.EventRecoveryJob, now, next, window time.Time, spent int) api.EventRecoveryPreflight {
	out := api.EventRecoveryPreflight{JobID: job.ID, ObservedAt: now, State: job.State, Active: eventRecoveryActive(job.State) && job.ExpiresAt.After(now), RatePerSecond: job.RatePerSecond, RemainingLifetimeSeconds: max(0, job.ExpiresAt.Sub(now).Seconds()), ReasonCounts: map[string]int64{}, CapacityScopes: map[string]int64{}, Sample: []api.EventRecoveryPreflightItem{}}
	out.AssumesImmediateResume = job.State == "paused" && out.Active
	pending := job.PendingCount
	finish := now
	if pending > 0 {
		start := maxRecoveryTime(now, next)
		end := window.Add(time.Second)
		available := int64(max(0, job.RatePerSecond-spent))
		if !end.After(start) {
			available = int64(job.RatePerSecond)
			end = start.Add(time.Second)
		}
		finish = start
		if pending > available {
			finish = end.Add(time.Duration((pending-available-1)/int64(job.RatePerSecond)) * time.Second)
		}
	}
	out.EarliestDrainAt = finish
	out.MinimumDrainSeconds = max(0, finish.Sub(now).Seconds())
	out.FitsBeforeExpiry = out.Active && finish.Before(job.ExpiresAt)
	return out
}
func addRecoveryPreflight(out *api.EventRecoveryPreflight, position int64, reason, scope string) {
	item := api.EventRecoveryPreflightItem{Position: position, Reason: reason, CapacityScope: scope}
	out.PendingCount++
	switch reason {
	case "eligible":
		item.Status = "eligible"
		out.EligibleCount++
	case "capacity", "legacy_claim":
		item.Status = "waiting"
		out.WaitingCount++
	case "unknown":
		item.Status = "unknown"
		out.UnknownCount++
	default:
		item.Status = "likely_skipped"
		out.LikelySkippedCount++
	}
	out.ReasonCounts[reason]++
	if scope != "" {
		out.CapacityScopes[scope]++
	}
	if len(out.Sample) < api.EventRecoveryItemsPageMax {
		out.Sample = append(out.Sample, item)
	}
}
func (s *PgStore) GetEventRecoveryPreflight(ctx context.Context, account, id string, now time.Time) (api.EventRecoveryPreflight, error) {
	if err := eventRecoveryIDs(account, id); err != nil {
		return api.EventRecoveryPreflight{}, err
	}
	if now.IsZero() {
		return api.EventRecoveryPreflight{}, ErrEventRecoveryQuery
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return api.EventRecoveryPreflight{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	job, err := getEventRecoveryMetadata(ctx, q, tx, account, id)
	if err != nil {
		return api.EventRecoveryPreflight{}, err
	}
	row, err := q.EventRecoveryPreflightJob(ctx, tx, sqlc.EventRecoveryPreflightJobParams{AccountID: mustPgUUID(account), JobID: mustPgUUID(id)})
	if err != nil {
		return api.EventRecoveryPreflight{}, err
	}
	out := newRecoveryPreflight(job, now, timeFromPgtype(row.NextAttemptAt), timeFromPgtype(row.WindowStartedAt), int(row.WindowCount))
	items, err := q.EventRecoveryPreflight(ctx, tx, sqlc.EventRecoveryPreflightParams{RetentionSeconds: int64(PublishedEventIdentityRetention / time.Second), JobCutoffAt: pgtypeFromTime(now.Add(-api.EventReplayBackfillJobRetention)), AccountID: mustPgUUID(account), JobID: mustPgUUID(id), NowAt: pgtypeFromTime(now), PageLimit: api.EventRecoveryRecipientsMax + 1})
	if err != nil {
		return out, err
	}
	if len(items) > api.EventRecoveryRecipientsMax {
		return out, ErrEventRecoveryQuery
	}
	for _, item := range items {
		reason, scope := item.Reason, ""
		if reason == "eligible" && item.CapacityTracked {
			limits, ok := api.LimitsFor(api.Plan(item.Plan))
			if !ok {
				reason = "unknown"
			} else {
				scope = eventCapacityScope(limits.EventDeliveries, item.ConsumerCount, item.AppCount, item.AccountCount)
				if scope != "" {
					reason = "capacity"
				}
			}
		}
		addRecoveryPreflight(&out, item.Position, reason, scope)
		addRecoveryReceiptRetention(&out, item.Position, timestamptzToTimePtr(item.ReceiptRetainUntil), item.ReceiptRetentionHeld)
	}
	return out, tx.Commit(ctx)
}

type recoveryPreflightCapacity struct {
	account, app int64
	consumers    map[string]int64
	limits       api.EventDeliveryLimits
}

func (m *MemStore) preflightCapacityLocked(job *memEventRecoveryJob) recoveryPreflightCapacity {
	acct, found := m.accounts[job.AccountID]
	if !found {
		acct = m.accounts[strings.ReplaceAll(job.AccountID, "-", "")]
	}
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok {
		limits = api.MustLimitsFor(api.PlanFree)
	}
	out := recoveryPreflightCapacity{limits: limits.EventDeliveries, consumers: map[string]int64{}}
	for id, slot := range m.eventDeliverySlots {
		inv, ok := m.invocations[id]
		if !ok || !sameMemUUID(slot.AccountID, job.AccountID) || inv.State != InvocationPending && inv.State != InvocationDispatching {
			continue
		}
		out.account++
		if sameMemUUID(slot.AppID, job.Job.AppID) {
			out.app++
			out.consumers[slot.SubscriptionID]++
		}
	}
	return out
}
func (m *MemStore) preflightItemLocked(job *memEventRecoveryJob, item memEventRecoveryItem, work *PublishedEventWork, capacity recoveryPreflightCapacity, now time.Time) (string, string, error) {
	if !m.eventRecoveryAppLocked(job.AccountID, job.Job.AppID) {
		return "target_unavailable", "", nil
	}
	var recipient *PublishedEventRecipient
	if work != nil && sameMemUUID(eventRoutingAccount(work), job.AccountID) {
		for _, r := range work.RecipientSnapshot {
			if r.ID == item.SubscriptionID && sameMemUUID(r.AppID, job.Job.AppID) {
				copy := r
				recipient = &copy
				break
			}
		}
		if job.Job.Selection.Mode == "execution" {
			if routed := work.routingRecipients[item.SubscriptionID]; routed != nil && sameMemUUID(routed.Recipient.AppID, job.Job.AppID) {
				copy := routed.Recipient
				recipient = &copy
			}
		}
	}
	if job.Job.Selection.Mode != "execution" {
		if recipient == nil {
			return "receipt_expired", "", nil
		}
		progress := work.RecipientProgress[item.SubscriptionID]
		encoded, err := memEventRecoveryIdentity(progress)
		if err != nil {
			return "", "", err
		}
		if !bytes.Equal(encoded, item.ExpectedProgress) || eventRecipientRoutingStateLocked(work, item.SubscriptionID) != PublishedEventRecipientFailed || work.RecipientClaims && work.routingRecipients[item.SubscriptionID] == nil {
			return "changed", "", nil
		}
		if !work.RecipientClaims && !work.Delivered && work.ClaimToken != "" {
			return "legacy_claim", "", nil
		}
		if EventDeliveryExpired(*recipient, work.CreatedAt, PublishedEventRecipientProgress{}, now) {
			return "expired", "", nil
		}
		return "eligible", "", nil
	}
	var identity executionRecoveryIdentity
	if err := json.Unmarshal(item.ExpectedProgress, &identity); err != nil {
		return "", "", err
	}
	parent, ok := m.invocations[identity.InvocationID]
	if !ok || !sameMemUUID(parent.AccountID, job.AccountID) || !sameMemUUID(parent.AppID, job.Job.AppID) || !identity.matches(parent) || m.plainReplayChildren[parent.ID].ChildID != "" || m.keyedReplayChildren[parent.ID] != "" {
		return "changed", "", nil
	}
	if parent.WorkExpiresAt != nil && !parent.WorkExpiresAt.After(now) || parent.StartDeadlineAt != nil && !parent.StartDeadlineAt.After(now) {
		return "expired", "", nil
	}
	if recipient == nil {
		return "receipt_expired", "", nil
	}
	if _, _, linked := m.operationForInvocationLocked(parent.ID); linked {
		parent.OperationID = "owned"
	}
	if parent.State == InvocationDeadLetter {
		if identity.DeadLetterID != unifiedDeadLetterEventID("invocation", parent.ID) || !m.productionInvocationWorkLocked(parent) {
			return "changed", "", nil
		}
		if _, purged := m.deadLetterPurged[identity.DeadLetterID]; purged {
			return "changed", "", nil
		}
	} else {
		if !plainReplayAllowed(parent) && !keyedReplayAllowed(parent) {
			return "changed", "", nil
		}
		replay := parent
		replay.Source = InvocationReplay
		if err := m.platformTenantInvocationAllowedLocked(replay); err != nil {
			if errors.Is(err, ErrPlatformTenantSuspended) {
				return "target_unavailable", "", nil
			}
			return "unknown", "", nil
		}
	}
	if slot, tracked := m.eventDeliverySlots[parent.ID]; tracked {
		scope := eventCapacityScope(capacity.limits, capacity.consumers[slot.SubscriptionID], capacity.app, capacity.account)
		if scope != "" {
			return "capacity", scope, nil
		}
	}
	return "eligible", "", nil
}
func (m *MemStore) GetEventRecoveryPreflight(ctx context.Context, account, id string, now time.Time) (api.EventRecoveryPreflight, error) {
	if err := ctx.Err(); err != nil {
		return api.EventRecoveryPreflight{}, err
	}
	if now.IsZero() {
		return api.EventRecoveryPreflight{}, ErrEventRecoveryQuery
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	job, err := m.eventRecoveryJobLocked(account, id)
	if err != nil {
		return api.EventRecoveryPreflight{}, err
	}
	metadata := job.Job
	metadata.PendingCount = 0
	items := []memEventRecoveryItem{}
	for _, item := range job.Items {
		if item.State == "pending" {
			items = append(items, item)
			metadata.PendingCount++
		}
	}
	if len(items) > api.EventRecoveryRecipientsMax {
		return api.EventRecoveryPreflight{}, ErrEventRecoveryQuery
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Position < items[j].Position })
	out := newRecoveryPreflight(metadata, now, job.NextAttemptAt, job.WindowStartedAt, job.WindowCount)
	receipts := map[int64]*PublishedEventWork{}
	for _, work := range m.eventFanout {
		receipts[work.ID] = work
	}
	capacity := m.preflightCapacityLocked(job)
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		reason, scope, err := m.preflightItemLocked(job, item, receipts[item.OutboxID], capacity, now)
		if err != nil {
			return out, err
		}
		addRecoveryPreflight(&out, item.Position, reason, scope)
		var until *time.Time
		if work := receipts[item.OutboxID]; work != nil && work.Delivered && !work.DeliveredAt.IsZero() {
			at := work.DeliveredAt.Add(PublishedEventIdentityRetention)
			until = &at
		}
		addRecoveryReceiptRetention(&out, item.Position, until, false)
	}
	return out, nil
}

// Retention is independent of the recovery job's expiry. Current backfill holds
// are observations, not a promise that the pin lasts through admission.
func addRecoveryReceiptRetention(out *api.EventRecoveryPreflight, position int64, until *time.Time, held bool) {
	if until == nil {
		return
	}
	if held {
		out.ReceiptRetentionHeldCount++
	} else {
		boundary := maxRecoveryTime(out.ObservedAt.Add(api.EventRetentionDefaultWindow), out.EarliestDrainAt)
		if !until.After(boundary) {
			out.ReceiptRetentionWarningCount++
		}
		if out.EarliestUnheldRetainUntil == nil || until.Before(*out.EarliestUnheldRetainUntil) {
			at := *until
			out.EarliestUnheldRetainUntil = &at
		}
		out.MinimumDrainCrossesReceiptRetention = out.EarliestUnheldRetainUntil != nil && !out.EarliestDrainAt.Before(*out.EarliestUnheldRetainUntil)
	}
	for i := range out.Sample {
		if out.Sample[i].Position == position {
			out.Sample[i].ReceiptRetainUntil = until
			out.Sample[i].ReceiptRetentionHeld = held
			break
		}
	}
}
