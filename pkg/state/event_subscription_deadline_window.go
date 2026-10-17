package state

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type EventSubscriptionDeadlineWindowStore interface {
	GetEventSubscriptionDeadlineWindow(context.Context, string, string, string, int64) (api.EventSubscriptionDeadlineWindow, error)
}

var _ EventSubscriptionDeadlineWindowStore = (*PgStore)(nil)
var _ EventSubscriptionDeadlineWindowStore = (*MemStore)(nil)

func (s *PgStore) GetEventSubscriptionDeadlineWindow(ctx context.Context, account, app, sub string, within int64) (api.EventSubscriptionDeadlineWindow, error) {
	var zero api.EventSubscriptionDeadlineWindow
	if validSubscriptionControlIDs(account, app, sub) != nil || !api.ValidEventSubscriptionDeadlineWindow(within) {
		return zero, ErrInvalidArgument
	}
	ctx, cancel := context.WithTimeout(ctx, api.EventSubscriptionDeadlineReadTimeout)
	defer cancel()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return zero, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err = q.EventSubscriptionPendingSummaryBudget(ctx, tx, strconv.Itoa(api.EventSubscriptionDeadlineStatementMillis)); err != nil {
		return zero, pendingSummaryError(err)
	}
	if _, err = q.EventConsumerPressureTarget(ctx, tx, sqlc.EventConsumerPressureTargetParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub)}); err != nil {
		return zero, pendingSummaryError(mapErr(err))
	}
	stamp, err := q.EventSubscriptionRateClock(ctx, tx)
	if err != nil {
		return zero, pendingSummaryError(err)
	}
	now := timeFromPgtype(stamp).UTC()
	st, err := readSubscriptionRatePreviewState(ctx, q, tx, account, app, sub, now)
	if err != nil {
		return zero, pendingSummaryError(err)
	}
	out := newSubscriptionDeadlineWindow(app, sub, within, st, now)
	rows, err := q.EventSubscriptionDeadlineWindowCounts(ctx, tx, sqlc.EventSubscriptionDeadlineWindowCountsParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: canonicalMemUUID(sub), ObservedAt: stamp, WindowEndAt: pgtypeFromTime(out.WindowEndAt)})
	if err != nil {
		return zero, pendingSummaryError(err)
	}
	for _, row := range rows {
		if row.PendingRecipients < 0 || row.PendingRecipients > api.EventSubscriptionPendingSummaryCountMax-out.Summary.PendingRecipients {
			return zero, errors.New("deadline window exceeds exact count representation")
		}
		out.Summary.PendingRecipients += row.PendingRecipients
		out.Summary.DeadlineRecipients += row.DeadlineRecipients
		out.Summary.AlreadyExpiredCount += row.ExpiredRecipients
		out.UpcomingRecipients += row.UpcomingRecipients
		if row.WaitingReason == "" {
			if row.ExpiredRecipients != 0 || row.UpcomingRecipients != 0 {
				return zero, errors.New("missing deadline waiting reason")
			}
		} else {
			if _, ok := out.UpcomingWaitingReasons[row.WaitingReason]; !ok {
				return zero, errors.New("unknown deadline waiting reason")
			}
			out.UpcomingWaitingReasons[row.WaitingReason] += row.UpcomingRecipients
			out.ExpiredWaitingReasons[row.WaitingReason] += row.ExpiredRecipients
		}
		mergeSubscriptionDeadlineMinimum(&out.Summary.EarliestDeadlineAt, timeFromPgtype(row.EarliestDeadlineAt))
		mergeSubscriptionDeadlineMinimum(&out.EarliestUpcomingDeadlineAt, timeFromPgtype(row.EarliestUpcomingDeadlineAt))
		finishSubscriptionDeadlineWindow(&out, row.ClockInvalid, row.ActiveLeases, row.ScheduledRetries)
	}
	if out.Summary.Complete && !api.EventSubscriptionDeadlineWindowCountsComplete(out) {
		return zero, errors.New("inconsistent deadline window counts")
	}
	if err = tx.Commit(ctx); err != nil {
		return zero, pendingSummaryError(err)
	}
	return out, nil
}
func newSubscriptionDeadlineWindow(app, sub string, within int64, st subscriptionRatePreviewState, now time.Time) api.EventSubscriptionDeadlineWindow {
	return api.EventSubscriptionDeadlineWindow{Coverage: "all_retained_captured_pending_application_deadline_window", Summary: newSubscriptionPendingSummary(app, sub, st, now), WithinMillis: within, WindowEndAt: now.Add(time.Duration(within) * time.Millisecond), UpcomingWaitingReasons: api.NewEventPendingWaitingReasonCounts(), ExpiredWaitingReasons: api.NewEventPendingWaitingReasonCounts()}
}
func mergeSubscriptionDeadlineMinimum(target **time.Time, at time.Time) {
	if !at.IsZero() && (*target == nil || at.Before(**target)) {
		copyTime := at.UTC()
		*target = &copyTime
	}
}
func finishSubscriptionDeadlineWindow(out *api.EventSubscriptionDeadlineWindow, invalid, leased, scheduled bool) {
	if invalid && out.Summary.Complete {
		out.Summary.Complete = false
		out.Summary.Constraints = append(out.Summary.Constraints, "clock_uncertain")
	}
	if leased && !slices.Contains(out.Summary.Constraints, "active_leases") {
		out.Summary.Constraints = append(out.Summary.Constraints, "active_leases")
	}
	if scheduled && !slices.Contains(out.Summary.Constraints, "scheduled_retries") {
		out.Summary.Constraints = append(out.Summary.Constraints, "scheduled_retries")
	}
}
