package state

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type EventConsumerHealthStore interface {
	GetEventConsumerHealth(context.Context, string, string, string, time.Time, time.Time) (api.EventConsumerHealth, error)
}

var _ EventConsumerHealthStore = (*PgStore)(nil)
var _ EventConsumerHealthStore = (*MemStore)(nil)

func validEventHealthWindow(since, now time.Time) bool {
	return !now.IsZero() && since.Before(now) && now.Sub(since) <= api.EventConsumerHealthMaxWindow
}
func finishConsumerHealth(out *api.EventConsumerHealth) {
	seconds := out.ObservedAt.Sub(out.WindowStart).Seconds()
	out.Coverage = EventRoutingHistoryCoverage
	out.RetryRatePerSecond = float64(out.RetryScheduled) / seconds
	out.DrainRatePerSecond = float64(out.SuccessfulRoutes) / seconds
	if n := out.SuccessfulRoutes + out.TerminalFailures; n > 0 {
		out.TerminalFailurePct = 100 * float64(out.TerminalFailures) / float64(n)
	}
	if out.OldestPendingAt != nil {
		out.OldestAgeSeconds = max(0, out.ObservedAt.Sub(*out.OldestPendingAt).Seconds())
	}
}
func (s *PgStore) GetEventConsumerHealth(ctx context.Context, account, app, sub string, since, now time.Time) (api.EventConsumerHealth, error) {
	out := api.EventConsumerHealth{ObservedAt: now, WindowStart: since}
	if err := validSubscriptionControlIDs(account, app, sub); err != nil {
		return out, err
	}
	if !validEventHealthWindow(since, now) {
		return out, ErrInvalidArgument
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err = q.EventSubscriptionControlTarget(ctx, tx, sqlc.EventSubscriptionControlTargetParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub)}); errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	} else if err != nil {
		return out, err
	}
	out.EventSubscriptionDeliveryControl, err = getSubscriptionControl(ctx, q, tx, account, app, sub)
	if err != nil {
		return out, err
	}
	control, err := q.EventSubscriptionControlGet(ctx, tx, sqlc.EventSubscriptionControlGetParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub)})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	if control.Paused && control.PausedAt.Valid {
		out.PausedSeconds = max(0, now.Sub(control.PausedAt.Time).Seconds())
	}
	h, err := q.EventConsumerHealthHistory(ctx, tx, sqlc.EventConsumerHealthHistoryParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: canonicalMemUUID(sub), SinceAt: pgtypeFromTime(since), NowAt: pgtypeFromTime(now)})
	if err != nil {
		return out, err
	}
	out.ExpiredDeliveries = h.ExpiredDeliveries
	out.SuccessfulRoutes, out.TerminalFailures, out.RetryScheduled, out.RoutingLatencyP95Seconds = h.SuccessfulRoutes, h.TerminalFailures, h.RetryScheduled, h.LatencyP95Seconds
	out.HistoryCompacted, err = q.EventConsumerHealthCompacted(ctx, tx, sqlc.EventConsumerHealthCompactedParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: canonicalMemUUID(sub), SinceAt: pgtypeFromTime(since)})
	if err != nil {
		return out, err
	}
	finishConsumerHealth(&out)
	return out, tx.Commit(ctx)
}
func (m *MemStore) GetEventConsumerHealth(ctx context.Context, account, app, sub string, since, now time.Time) (api.EventConsumerHealth, error) {
	out := api.EventConsumerHealth{ObservedAt: now, WindowStart: since}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if err := validSubscriptionControlIDs(account, app, sub); err != nil {
		return out, err
	}
	if !validEventHealthWindow(since, now) {
		return out, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.eventSubscriptionControlTargetLocked(account, app, sub) {
		return out, ErrNotFound
	}
	out.EventSubscriptionDeliveryControl = m.eventSubscriptionControlResponseLocked(account, app, sub)
	if c := m.eventSubscriptionControls[canonicalMemUUID(sub)]; c != nil && c.Paused {
		out.PausedSeconds = max(0, now.Sub(c.PausedAt).Seconds())
	}
	receipts := map[int64]*PublishedEventWork{}
	for _, r := range m.eventFanout {
		receipts[r.ID] = r
	}
	latencies := []float64{}
	for _, h := range m.eventFanoutAttempts {
		receipt := receipts[h.OutboxID]
		if receipt == nil || !sameMemUUID(eventRoutingAccount(receipt), account) || !sameMemUUID(h.AppID, app) || !sameMemUUID(h.SubscriptionID, sub) || h.OccurredAt.Before(since) || h.OccurredAt.After(now) || h.Action != "fanout_attempt" && h.Action != "backfill_attempt" {
			continue
		}
		switch h.State {
		case "enqueued":
			out.SuccessfulRoutes++
			latencies = append(latencies, max(0, h.OccurredAt.Sub(receipt.CreatedAt).Seconds()))
		case "failed":
			out.TerminalFailures++
			if h.FailureCode == EventFanoutFailureCodeDeliveryExpired {
				out.ExpiredDeliveries++
			}
		case "pending":
			if h.LastError != "" && h.CapacityScope == "" {
				out.RetryScheduled++
			}
		}
	}
	for key, h := range m.eventFanoutHistorySummaries {
		receipt := receipts[key.outboxID]
		if receipt != nil && sameMemUUID(eventRoutingAccount(receipt), account) && sameMemUUID(h.SubscriptionID, sub) && h.CompactedThroughAt != nil && !h.CompactedThroughAt.Before(since) {
			out.HistoryCompacted = true
		}
	}
	sort.Float64s(latencies)
	if n := len(latencies); n > 0 {
		pos := float64(n-1) * 0.95
		lo := int(pos)
		out.RoutingLatencyP95Seconds = latencies[lo]
		if lo+1 < n {
			out.RoutingLatencyP95Seconds += (latencies[lo+1] - latencies[lo]) * (pos - float64(lo))
		}
	}
	finishConsumerHealth(&out)
	return out, nil
}

func validEventConsumerAlertRule(r AlertRule) bool {
	if api.IsEventRecoveryAlertMetric(string(r.Metric)) {
		_, err := api.EventConsumerHealthWindow(string(r.WindowSpec))
		return r.AppID != "" && r.EventSubscriptionID == "" && (r.Action == "" || r.Action == AlertActionWebhook) && err == nil && r.WindowSpec != ""
	}
	if !api.IsEventConsumerAlertMetric(string(r.Metric)) {
		return r.EventSubscriptionID == ""
	}
	if r.AppID == "" || r.Action != "" && r.Action != AlertActionWebhook {
		return false
	}
	if _, err := uuid.Parse(r.EventSubscriptionID); err != nil {
		return false
	}
	_, err := api.EventConsumerHealthWindow(string(r.WindowSpec))
	return err == nil && r.WindowSpec != ""
}
