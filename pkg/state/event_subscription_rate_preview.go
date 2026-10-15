package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type EventSubscriptionRatePreviewStore interface {
	PreviewEventSubscriptionRateLimit(context.Context, string, string, string, api.EventSubscriptionRatePreviewRequest) (api.EventSubscriptionRatePreview, error)
}

var _ EventSubscriptionRatePreviewStore = (*PgStore)(nil)
var _ EventSubscriptionRatePreviewStore = (*MemStore)(nil)

type subscriptionRatePreviewPending struct {
	source, id                    string
	accepted, available, deadline time.Time
	outbox                        int64
	invalid, leased               bool
}
type subscriptionRatePreviewState struct {
	current      api.EventSubscriptionRateLimit
	credit       int64
	paused       bool
	rate, count  int
	window       time.Time
	circuit      string
	capacity     []string
	clockInvalid bool
}

func (s *PgStore) PreviewEventSubscriptionRateLimit(ctx context.Context, account, app, sub string, req api.EventSubscriptionRatePreviewRequest) (api.EventSubscriptionRatePreview, error) {
	var zero api.EventSubscriptionRatePreview
	if req.Validate() != nil || validSubscriptionControlIDs(account, app, sub) != nil {
		return zero, ErrInvalidArgument
	}
	enabled := *req.Enabled
	req.Enabled = &enabled
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return zero, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err = q.EventConsumerPressureTarget(ctx, tx, sqlc.EventConsumerPressureTargetParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub)}); err != nil {
		return zero, mapErr(err)
	}
	stamp, err := q.EventSubscriptionRateClock(ctx, tx)
	if err != nil {
		return zero, err
	}
	now := timeFromPgtype(stamp)
	st, err := readSubscriptionRatePreviewState(ctx, q, tx, account, app, sub, now)
	if err != nil {
		return zero, err
	}
	rows, err := q.EventSubscriptionRatePreviewPending(ctx, tx, sqlc.EventSubscriptionRatePreviewPendingParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: canonicalMemUUID(sub), SampleLimit: int32(api.EventSubscriptionRatePreviewSampleMax + 1)})
	if err != nil {
		return zero, err
	}
	pending := make([]subscriptionRatePreviewPending, 0, len(rows))
	for _, row := range rows {
		pending = append(pending, subscriptionRatePreviewPending{source: row.EventSource, id: row.EventID, accepted: timeFromPgtype(row.AcceptedAt), available: timeFromPgtype(row.NextAttemptAt), deadline: timeFromPgtype(row.DeadlineAt), outbox: row.OutboxID,
			invalid: !row.AcceptedAt.Valid || row.AcceptedAt.InfinityModifier != pgtype.Finite || row.NextAttemptAt.Valid && row.NextAttemptAt.InfinityModifier != pgtype.Finite || row.DeadlineAt.Valid && row.DeadlineAt.InfinityModifier != pgtype.Finite || row.LeaseUntil.Valid && row.LeaseUntil.InfinityModifier != pgtype.Finite,
			leased:  row.LeaseUntil.Valid && timeFromPgtype(row.LeaseUntil).After(now)})
	}
	out := buildSubscriptionRatePreview(app, sub, req, st, pending, now)
	if err = tx.Commit(ctx); err != nil {
		return zero, err
	}
	return out, nil
}
func readSubscriptionRatePreviewState(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, account, app, sub string, now time.Time) (subscriptionRatePreviewState, error) {
	var st subscriptionRatePreviewState
	st.current, st.credit, _ = subscriptionRateStatus(false, 0, 1000, 1, 1000, now, now)
	row, err := q.EventSubscriptionControlGet(ctx, tx, sqlc.EventSubscriptionControlGetParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub)})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return st, err
	}
	configured := 0
	if err == nil {
		st.current, st.credit, _ = subscriptionRateSQL(row, now)
		st.paused = row.Paused
		st.rate = int(row.RatePerSecond)
		st.count = int(row.WindowCount)
		st.window = timeFromPgtype(row.WindowStartedAt)
		configured = int(row.MaxPendingDeliveries)
		st.clockInvalid = row.RateEnabled && timeFromPgtype(row.RateRefilledAt).After(now) || row.RatePerSecond > 0 && timeFromPgtype(row.WindowStartedAt).After(now)
	}
	circuit, err := readCircuit(ctx, q, tx, account, app, sub)
	if err != nil {
		return st, err
	}
	st.circuit = "closed"
	if circuit != nil {
		st.circuit = circuit.Runtime.State
	}
	acct, err := q.AccountByID(ctx, tx, mustPgUUID(account))
	if err != nil {
		return st, mapErr(err)
	}
	limits, ok := api.LimitsFor(api.Plan(acct.Plan))
	if !ok {
		return st, ErrInvalidArgument
	}
	counts, err := q.EventDeliveryCounts(ctx, tx, sqlc.EventDeliveryCountsParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: canonicalMemUUID(sub)})
	if err != nil {
		return st, err
	}
	st.capacity = subscriptionRatePreviewCapacity(limits.EventDeliveries, configured, [3]int64{counts.ConsumerCount, counts.AppCount, counts.AccountCount})
	return st, nil
}
func subscriptionRatePreviewCapacity(limits api.EventDeliveryLimits, configured int, live [3]int64) []string {
	ceilings := [3]int{effectiveEventConsumerCapacity(limits.PerConsumer, configured), limits.PerApp, limits.PerAccount}
	names := [3]string{"consumer", "app", "account"}
	out := []string{}
	for i, name := range names {
		if live[i] >= int64(ceilings[i]) {
			out = append(out, name)
		}
	}
	return out
}

// This deterministic oldest-first model predicts rate-only admission times, not
// scheduler ordering, execution completion, or guarantees that deadlines survive.
func buildSubscriptionRatePreview(app, sub string, req api.EventSubscriptionRatePreviewRequest, st subscriptionRatePreviewState, pending []subscriptionRatePreviewPending, now time.Time) api.EventSubscriptionRatePreview {
	credit := req.IntervalMillis * req.Burst
	if st.current.Enabled && *req.Enabled {
		credit = min(credit, st.credit*req.IntervalMillis/st.current.IntervalMillis)
	}
	projected, _, _ := subscriptionRateStatus(*req.Enabled, st.current.Revision, req.IntervalMillis, req.Burst, credit, now, now)
	deadlineCount := 0
	out := api.EventSubscriptionRatePreview{DeadlineRecipients: &deadlineCount, DeadlineCoverage: "sampled_effective_pending_deadlines", Coverage: "retained_captured_pending_application_recipients", AppID: canonicalMemUUID(app), SubscriptionID: canonicalMemUUID(sub), ObservedAt: now, ReadOnly: true, Advisory: true, ExpectedRevision: st.current.Revision, Current: st.current, Projected: projected, Paused: st.paused, LegacyRatePerSecond: st.rate, CircuitState: st.circuit, CapacityBlockingScopes: st.capacity, Constraints: []string{}, SampleLimit: api.EventSubscriptionRatePreviewSampleMax, DeadlineRisks: []api.EventSubscriptionRateDeadlineRisk{}}
	if len(pending) > out.SampleLimit {
		out.Truncated = true
		pending = pending[:out.SampleLimit]
	}
	for _, item := range pending {
		if item.invalid || item.accepted.IsZero() || item.accepted.After(now) {
			st.clockInvalid = true
		}
	}
	out.SampledPending = len(pending)
	out.Complete = !out.Truncated && !st.clockInvalid
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
	if out.Truncated {
		out.Constraints = append(out.Constraints, "sample_truncated")
	}
	last, anchor, window, count := now, now, st.window, st.count
	scheduled, leased := false, false
	for _, item := range pending {
		if item.invalid || item.accepted.After(now) || item.accepted.IsZero() {
			out.Complete = false
			st.clockInvalid = true
			continue
		}
		if !item.deadline.IsZero() {
			deadlineCount++
			if out.EarliestDeadlineAt == nil || item.deadline.Before(*out.EarliestDeadlineAt) {
				deadline := item.deadline
				out.EarliestDeadlineAt = &deadline
			}
		}
		if item.leased {
			leased = true
		}
		if item.available.After(now) {
			scheduled = true
		}
		if !item.deadline.IsZero() && !item.deadline.After(now) {
			out.AlreadyExpiredCount++
			addSubscriptionRateRisk(&out, item, time.Time{}, "already_expired")
			continue
		}
		if st.clockInvalid {
			continue
		}
		at := last
		if item.available.After(at) {
			at = item.available
		}
		if st.rate > 0 && count >= st.rate && window.Add(time.Second).After(at) {
			at = window.Add(time.Second)
		}
		if projected.Enabled {
			_, credit, anchor = subscriptionRateStatus(true, projected.Revision, req.IntervalMillis, req.Burst, credit, anchor, at)
			if credit < req.IntervalMillis {
				at = anchor.Add(time.Duration(req.IntervalMillis-credit) * time.Millisecond)
			}
			_, credit, anchor = subscriptionRateStatus(true, projected.Revision, req.IntervalMillis, req.Burst, credit, anchor, at)
			credit -= req.IntervalMillis
		}
		if st.rate > 0 {
			if !window.Add(time.Second).After(at) {
				window = at
				count = 0
			}
			count++
		}
		last = at
		if !item.deadline.IsZero() && !at.Before(item.deadline) {
			addSubscriptionRateRisk(&out, item, at, "modeled_deadline_risk")
		}
	}
	if leased {
		out.Constraints = append(out.Constraints, "active_leases")
	}
	if scheduled {
		out.Constraints = append(out.Constraints, "scheduled_retries")
	}
	if !st.clockInvalid {
		duration := last.Sub(now)
		if duration == time.Duration(1<<63-1) {
			out.Complete = false
			out.Constraints = append(out.Constraints, "estimate_overflow")
		} else {
			value := duration.Milliseconds()
			if duration%time.Millisecond != 0 {
				value++
			}
			out.RateOnlyDrainMillis = &value
		}
	}
	return out
}

func addSubscriptionRateRisk(out *api.EventSubscriptionRatePreview, item subscriptionRatePreviewPending, at time.Time, reason string) {
	out.DeadlineRiskCount++
	if len(out.DeadlineRisks) >= api.EventSubscriptionRatePreviewRiskMax {
		out.RisksTruncated = true
		return
	}
	risk := api.EventSubscriptionRateDeadlineRisk{EventSource: item.source, EventID: item.id, AcceptedAt: item.accepted, DeadlineAt: item.deadline, Reason: reason}
	if !at.IsZero() {
		risk.ModeledAdmissionAt = &at
	}
	out.DeadlineRisks = append(out.DeadlineRisks, risk)
}
