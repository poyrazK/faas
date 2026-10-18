package state

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type EventSubscriptionPendingSummaryStore interface {
	GetEventSubscriptionPendingSummary(context.Context, string, string, string) (api.EventSubscriptionPendingSummary, error)
}

var _ EventSubscriptionPendingSummaryStore = (*PgStore)(nil)
var _ EventSubscriptionPendingSummaryStore = (*MemStore)(nil)

func (s *PgStore) GetEventSubscriptionPendingSummary(ctx context.Context, account, app, sub string) (api.EventSubscriptionPendingSummary, error) {
	var zero api.EventSubscriptionPendingSummary
	if validSubscriptionControlIDs(account, app, sub) != nil {
		return zero, ErrInvalidArgument
	}
	ctx, cancel := context.WithTimeout(ctx, api.EventSubscriptionPendingSummaryTimeout)
	defer cancel()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return zero, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err = q.EventSubscriptionPendingSummaryBudget(ctx, tx, strconv.Itoa(api.EventSubscriptionPendingSummaryStatementMillis)); err != nil {
		return zero, pendingSummaryError(err)
	}
	if _, err = q.EventConsumerPressureTarget(ctx, tx, sqlc.EventConsumerPressureTargetParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub)}); err != nil {
		return zero, pendingSummaryError(mapErr(err))
	}
	stamp, err := q.EventSubscriptionRateClock(ctx, tx)
	if err != nil {
		return zero, pendingSummaryError(err)
	}
	now := timeFromPgtype(stamp)
	st, err := readSubscriptionRatePreviewState(ctx, q, tx, account, app, sub, now)
	if err != nil {
		return zero, pendingSummaryError(err)
	}
	row, err := q.EventSubscriptionPendingSummary(ctx, tx, sqlc.EventSubscriptionPendingSummaryParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: canonicalMemUUID(sub), ObservedAt: stamp})
	if err != nil {
		return zero, pendingSummaryError(err)
	}
	if row.PendingRecipients > api.EventSubscriptionPendingSummaryCountMax {
		return zero, errors.New("pending summary exceeds exact count representation")
	}
	out := newSubscriptionPendingSummary(app, sub, st, now)
	out.PendingRecipients = row.PendingRecipients
	out.DeadlineRecipients = row.DeadlineRecipients
	out.AlreadyExpiredCount = row.AlreadyExpiredCount
	if row.EarliestDeadlineAt.Valid {
		deadline := timeFromPgtype(row.EarliestDeadlineAt)
		out.EarliestDeadlineAt = &deadline
	}
	finishSubscriptionPendingSummary(&out, row.ClockInvalid, row.ActiveLeases, row.ScheduledRetries)
	if err = tx.Commit(ctx); err != nil {
		return zero, pendingSummaryError(err)
	}
	return out, nil
}

func pendingSummaryError(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "57014" {
		return context.DeadlineExceeded
	}
	return err
}
func newSubscriptionPendingSummary(app, sub string, st subscriptionRatePreviewState, now time.Time) api.EventSubscriptionPendingSummary {
	out := api.EventSubscriptionPendingSummary{Coverage: "all_retained_captured_pending_application_recipients", AppID: canonicalMemUUID(app), SubscriptionID: canonicalMemUUID(sub), ObservedAt: now, ReadOnly: true, Complete: !st.clockInvalid, Current: st.current, Paused: st.paused, LegacyRatePerSecond: st.rate, CircuitState: st.circuit, CapacityBlockingScopes: st.capacity, Constraints: []string{}}
	if st.paused {
		out.Constraints = append(out.Constraints, "subscription_paused")
	}
	if st.circuit != "closed" {
		out.Constraints = append(out.Constraints, "circuit_"+st.circuit)
	}
	if st.rate > 0 {
		out.Constraints = append(out.Constraints, "legacy_rate_limit")
	}
	for _, scope := range st.capacity {
		out.Constraints = append(out.Constraints, "capacity_"+scope)
	}
	if st.clockInvalid {
		out.Constraints = append(out.Constraints, "clock_uncertain")
	}
	return out
}
func finishSubscriptionPendingSummary(out *api.EventSubscriptionPendingSummary, invalid, leased, scheduled bool) {
	if invalid && out.Complete {
		out.Complete = false
		out.Constraints = append(out.Constraints, "clock_uncertain")
	}
	if leased {
		out.Constraints = append(out.Constraints, "active_leases")
	}
	if scheduled {
		out.Constraints = append(out.Constraints, "scheduled_retries")
	}
}
