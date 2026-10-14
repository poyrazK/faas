package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type EventConsumerCapacityPressureStore interface {
	GetEventConsumerCapacityPressure(context.Context, string, string, string) (api.EventConsumerCapacityPressure, error)
}

var _ EventConsumerCapacityPressureStore = (*PgStore)(nil)
var _ EventConsumerCapacityPressureStore = (*MemStore)(nil)

func buildEventConsumerCapacityPressure(app, sub string, now time.Time, limits api.EventDeliveryLimits, configured int, live [3]int64, waiting [3]int64, oldest [3]*time.Time) api.EventConsumerCapacityPressure {
	planCeiling := limits.PerConsumer
	limits.PerConsumer = effectiveEventConsumerCapacity(limits.PerConsumer, configured)
	out := api.EventConsumerCapacityPressure{PlanMaxPendingDeliveries: planCeiling, AppID: canonicalMemUUID(app), SubscriptionID: canonicalMemUUID(sub), ObservedAt: now, Coverage: "live_slots_and_all_pending_application_recipients", Complete: true, ConfiguredMaxPendingDeliveries: configured, BlockingScope: eventCapacityScope(limits, live[0], live[1], live[2]), Scopes: make([]api.EventConsumerCapacityScope, 0, 3)}
	names := [3]string{"consumer", "app", "account"}
	ceilings := [3]int{limits.PerConsumer, limits.PerApp, limits.PerAccount}
	advice := [3]string{
		"Inspect this consumer's pending and dispatching deliveries; let them drain or review its configured ceiling within the plan allowance.",
		"Inspect sibling consumer backlogs in this app and let live deliveries drain; raising only this subscription's ceiling cannot remove an app limit.",
		"Inspect delivery backlogs across owned apps and let live deliveries drain; raising only this subscription's ceiling cannot remove an account limit.",
	}
	for index, scope := range names {
		row := api.EventConsumerCapacityScope{Scope: scope, LiveDeliveries: live[index], EffectiveLimit: ceilings[index], AtLimit: live[index] >= int64(ceilings[index]), WaitingRecipients: waiting[index], OldestWaitingAcceptedAt: oldest[index]}
		if oldest[index] != nil {
			row.OldestWaitingAgeSeconds = max(0, now.Sub(*oldest[index]).Seconds())
		}
		if row.AtLimit {
			row.Advice = advice[index]
		} else if row.WaitingRecipients > 0 {
			row.Advice = "Refresh health; live capacity may have changed since these recipients recorded their wait."
		} else {
			row.Advice = "No observed pressure at this scope; continue monitoring."
		}
		out.Scopes = append(out.Scopes, row)
	}
	return out
}

func (s *PgStore) GetEventConsumerCapacityPressure(ctx context.Context, account, app, sub string) (api.EventConsumerCapacityPressure, error) {
	var out api.EventConsumerCapacityPressure
	if err := validSubscriptionControlIDs(account, app, sub); err != nil {
		return out, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	now := time.Now().UTC()
	q := sqlc.New()
	if _, err = q.EventConsumerPressureTarget(ctx, tx, sqlc.EventConsumerPressureTargetParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub)}); errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	} else if err != nil {
		return out, err
	}
	acct, err := q.AccountByID(ctx, tx, mustPgUUID(account))
	if err != nil {
		return out, mapErr(err)
	}
	limits, ok := api.LimitsFor(api.Plan(acct.Plan))
	if !ok {
		return out, ErrInvalidArgument
	}
	configured, err := q.EventSubscriptionCapacityOverride(ctx, tx, sqlc.EventSubscriptionCapacityOverrideParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: canonicalMemUUID(sub)})
	if err != nil {
		return out, err
	}
	counts, err := q.EventDeliveryCounts(ctx, tx, sqlc.EventDeliveryCountsParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: canonicalMemUUID(sub)})
	if err != nil {
		return out, err
	}
	waits, err := q.EventConsumerPressureWaits(ctx, tx, sqlc.EventConsumerPressureWaitsParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: canonicalMemUUID(sub), ObservedAt: pgtypeFromTime(now)})
	if err != nil {
		return out, err
	}
	out = buildEventConsumerCapacityPressure(app, sub, now, limits.EventDeliveries, int(configured), [3]int64{counts.ConsumerCount, counts.AppCount, counts.AccountCount}, [3]int64{waits.ConsumerWaiting, waits.AppWaiting, waits.AccountWaiting}, [3]*time.Time{timestamptzToTimePtr(waits.ConsumerOldest), timestamptzToTimePtr(waits.AppOldest), timestamptzToTimePtr(waits.AccountOldest)})
	out.CapacityCommandBudget, err = readCapacityCommandBudget(ctx, q, tx, account, app, sub, now)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

func (m *MemStore) GetEventConsumerCapacityPressure(ctx context.Context, account, app, sub string) (api.EventConsumerCapacityPressure, error) {
	var out api.EventConsumerCapacityPressure
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if err := validSubscriptionControlIDs(account, app, sub); err != nil {
		return out, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if !m.eventSubscriptionControlTargetLocked(account, app, sub) {
		return out, ErrNotFound
	}
	now := time.Now().UTC()
	configured := 0
	if c := m.eventSubscriptionControls[canonicalMemUUID(sub)]; c != nil && sameMemUUID(c.AccountID, account) && sameMemUUID(c.AppID, app) {
		configured = c.MaxPendingDeliveries
	}
	consumer, application, acct := m.eventDeliveryCountsLocked(eventDeliverySlot{account, app, canonicalMemUUID(sub)}, nil)
	waiting, oldest, err := m.eventConsumerPressureWaitsLocked(ctx, account, app, sub, now)
	if err != nil {
		return out, err
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	out = buildEventConsumerCapacityPressure(app, sub, now, m.eventDeliveryLimitsLocked(account), configured, [3]int64{consumer, application, acct}, waiting, oldest)
	out.CapacityCommandBudget, err = m.capacityCommandBudgetLocked(ctx, account, app, sub, now)
	return out, err
}

func (m *MemStore) eventConsumerPressureWaitsLocked(ctx context.Context, account, app, sub string, now time.Time) ([3]int64, [3]*time.Time, error) {
	var counts [3]int64
	var oldest [3]*time.Time
	for _, work := range m.eventFanout {
		if err := ctx.Err(); err != nil {
			return counts, oldest, err
		}
		if work.Delivered || !work.SnapshotCaptured || work.CreatedAt.After(now) || !sameMemUUID(eventRoutingAccount(work), account) {
			continue
		}
		for index, recipient := range work.RecipientSnapshot {
			if !sameMemUUID(recipient.AppID, app) || recipient.ID != canonicalMemUUID(sub) {
				continue
			}
			var identity publishedEventIdentity
			if err := json.Unmarshal(work.Payload, &identity); err != nil {
				return counts, oldest, err
			}
			entry := m.backlogRecipientLocked(account, work, recipient, identity)
			if entry.State != "pending" || entry.ConsumerKind != "application" {
				continue
			}
			if prior, _ := m.orderedEventRecipientBlockerLocked(work, index, work.RecipientClaims); prior != nil {
				entry.OrderingBlocker = &api.EventOrderingBlocker{}
			}
			// Match the SQL backlog rule: an active whole-event lease suppresses
			// current pause/circuit overrides while preserving the recorded reason.
			if entry.RoutingMode != "event" || entry.LeaseUntil == nil || !entry.LeaseUntil.After(now) {
				entry.DeliveryControlReason = m.eventSubscriptionWaitingReasonLocked(account, recipient.AppID, recipient.ID, now)
			}
			backlogObservation(&entry.EventBacklogRecipient, now)
			var count *int64
			var first **time.Time
			switch entry.WaitingReason {
			case "capacity_consumer":
				count, first = &counts[0], &oldest[0]
			case "capacity_app":
				count, first = &counts[1], &oldest[1]
			case "capacity_account":
				count, first = &counts[2], &oldest[2]
			default:
				continue
			}
			*count++
			if *first == nil || entry.AcceptedAt.Before(**first) {
				accepted := entry.AcceptedAt
				*first = &accepted
			}
		}
	}
	return counts, oldest, nil
}
