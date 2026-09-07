// pkg/sched/operator_intent_completeness.go — schedd 60s tick
// that drives the operator-action observability counters behind
// GET /v1/admin/obs/health (PR-#TBD / C5).
//
// One query, over the local pool:
//
//   - Trace_id completeness ratio per kind (the gauge
//     operatorActionTraceCompletenessRatio<kind>).
//     SELECT kind, count(*) FILTER (WHERE trace_id IS NOT
//     NULL)::float / count(*) FROM events WHERE kind LIKE
//     'operator.action.%' AND at > now() - interval
//     '5 minutes' GROUP BY kind. Kinds absent from the result
//     are treated as 1.0 (no rows ⇒ vacuously complete) so the
//     pre-instantiated gauge surfaces zero only when there is
//     evidence of a coverage gap. The window predicate reads
//     `at`, NOT `received_at` — `events.at` is the canonical
//     timestamp column (migrations/00001_init.sql:132;
//     `received_at` belongs to compute_node_heartbeats and
//     audit_log, different tables). The previous
//     received_at-shaped predicate raised "column does not
//     exist" at runtime and zeroed the gauge via the catch-all
//     best-effort log path; the query now reads `at` and the
//     ratio surfaces a real value.
//
// History: this file used to drive a SECOND counter
// (operatorIntentOutcomeMissingTotal) that polled
// operator_intents for rows stuck in `running` past the safety-
// tick threshold. That counter raced the safety tick's reclaim
// AND duplicated the Store.OperatorIntentOutcomeMissingCounts
// surface used by the operator-side obs health endpoint, so the
// fix-cluster (#1111 review-finding #5) deleted it. Stuck-
// running observability now flows through the Store method
// alone — the same path the apid /obs/health handler reads on
// each request.
//
// The query is read-only and bypasses the engine — schedd is
// already the operator_intents writer (CLAUDE.md ownership) so
// the read is on the same connection as the existing safety
// tick's writes. The existing safety tick's reclaim runs on a
// separate 30s cadence; the completeness tick is a sidecar that
// never writes, only observes.
//
// The tick is nil-safe: when l.ops is nil (legacy / tests that
// haven't wired OpsMetrics), the body short-circuits and returns.
// The query itself still runs (it's cheap) so the test surface
// can observe state without a registry.

package sched

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// operatorIntentCompletenessTick is the 60s cadence for the
// PR-#TBD / C5 observability counters. Slower than the
// existing operatorIntentSafetyTick (30s) because the queries
// behind it are read-only and window-aggregated; a 60s sweep
// adds ~720 SELECTs/hour of operator_intents load on the
// shared pool, which is at the cheap end of the
// ~720/h partition that the existing safety tick already
// pays for.
//
// The cadence is independent of the safety tick so the two
// never race on the same row: the safety tick reclaims
// stuck-running rows back to pending; the completeness tick
// observes the count and moves on. No coordination needed.
const operatorIntentCompletenessTick = 60 * time.Second

// operatorIntentCompletenessWindow bounds the events-side
// aggregation. 5 minutes is the operator-action SLA window —
// anything older is not relevant to "is the trace_id path
// healthy right now?" and would dilute the ratio with stale
// rows from before the C4 middleware rollout.
const operatorIntentCompletenessWindow = 5 * time.Minute

// runOperatorIntentCompletenessTick is the per-60s body.
// Cheap (~2 SELECTs, both bounded by partial indexes on
// `events.trace_id` and `operator_intents.status`/`started_at`).
// Errors are logged and swallowed — the metric observation is
// best-effort; the safety tick's own recovery primitive keeps
// the queue draining even if the obs tick stalls.
//
// The function is exposed (not unexported) because the
// sched_test.go suite drives it directly to assert the
// per-tick math without spinning up a full ticker.
func (l *Loop) runOperatorIntentCompletenessTick(ctx context.Context) {
	if l.ops == nil {
		// Legacy / opt-out path. Still run the queries so the
		// test surface can observe them via a hand-wired
		// state.Store, but the gauge / counter writes are
		// short-circuited at the accessor level (m == nil
		// guards).
		_ = l.observeOperatorIntentCompleteness(ctx)
		return
	}
	l.observeOperatorIntentCompleteness(ctx)
}

// observeOperatorIntentCompleteness runs both queries and
// pushes the results into the wire.OpsMetrics gauges /
// counters. Returns the number of gauge updates + counter
// increments so the unit tests can assert the call count
// without parsing the Prometheus registry.
//
// Pre-instantiation grid (pkg/wire/metrics.go:3140-3151)
// guarantees every (endpoint, kind) combination is already
// materialised in the registry, so WithLabelValues never
// allocates a new label set at runtime — the ForLabelValues
// call is a map lookup.
//
// Both SELECTs use kind labels from the closed set declared
// in pkg/wire (auditKindClosedSet for events, operatorIntentKindClosedSet
// for operator_intents). A kind that has zero rows in the
// window is treated as completeness=1.0 — vacuous truth, same
// posture the existing /v1/admin/obs/health endpoint takes
// for absent verb metadata. The pre-instantiated gauge
// surfaces 1.0 for those kinds, so /health's ratio field
// reads 1.0 for "no traffic" rather than 0.0 for "100% broken".
func (l *Loop) observeOperatorIntentCompleteness(ctx context.Context) (gaugeUpdates int) {
	if err := l.observeTraceCompletenessRatio(ctx, &gaugeUpdates); err != nil {
		l.log.Warn("sched: operator_intent_completeness: trace ratio query failed",
			"err", err)
		return gaugeUpdates
	}
	if l.ops != nil {
		l.ops.SetOperatorActionTraceCompletenessLastSuccess(time.Now())
		l.ops.MarkOperatorActionTraceCompletenessFirstTickCompleted()
	}
	return gaugeUpdates
}

// observeTraceCompletenessRatio runs the events-side
// aggregation and sets the per-kind gauge.
//
// SQL is hand-written here (not sqlc) because:
//
//   - It's a one-off metric query, not a state machine read
//     or a writer (CLAUDE.md: "SQL via sqlc only; no string-
//     built queries" is about state writes; metric queries
//     use the same hand-written pattern as
//     pkg/state/pgstore.go::ReclaimStuckRunningOperatorIntents).
//   - The aggregation is windowed + kind-grouped, which the
//     sqlc interface would force through a scan-into-struct
//     pattern that's worse than reading pgx.Rows directly
//     here.
//
// The query is parameterised on the window interval (a
// time.Duration cast to seconds, not a string concatenation)
// so the SQL injection surface is empty.
func (l *Loop) observeTraceCompletenessRatio(ctx context.Context, gaugeUpdates *int) error {
	// Closed kind set — see auditKindClosedSet in
	// pkg/wire/metrics.go. Mirror the structure here so a
	// typo in one place shows up in test diffs, not in a
	// silent gauge label. Includes the apid request-side
	// instance-oriented aliases ("park_instance",
	// "restart_instance", "*.outcome") — the gauge's
	// prefix-strip pass (below) hands us the post-strip form,
	// and the kindClosedSet lookup must recognise both the
	// verb-oriented (schedd outcome emits) and the
	// instance-oriented (apid request emits) shapes so a
	// single ratio computation covers both surfaces.
	kindClosedSet := []string{
		"force_park",
		"force_cold_boot",
		"force_restart",
		"force_park.outcome",
		"force_cold_boot.outcome",
		"force_restart.outcome",
		"park_instance",
		"park_instance.outcome",
		"restart_instance",
		"restart_instance.outcome",
	}
	// Pre-fill with 1.0 (vacuous truth — no rows in window
	// ⇒ 100% complete). The pre-instantiated gauge surfaces
	// this default so /health's ratio field reads 1.0 for
	// "no traffic" rather than 0.0 for "100% broken".
	ratios := make(map[string]float64, len(kindClosedSet))
	for _, k := range kindClosedSet {
		ratios[k] = 1.0
	}

	rows, err := l.pool.Query(ctx, `
		SELECT
		    kind,
		    CASE WHEN count(*) = 0 THEN 1.0
		         ELSE count(*) FILTER (WHERE trace_id IS NOT NULL)::float / count(*)
		    END AS ratio
		FROM events
		WHERE kind LIKE 'operator.action.%'
		  AND at > now() - ($1::bigint * interval '1 second')
		GROUP BY kind
	`, int64(operatorIntentCompletenessWindow.Seconds()))
	if err != nil {
		return fmt.Errorf("query trace completeness ratio: %w", err)
	}
	defer rows.Close()

	type row struct {
		Kind  string
		Ratio float64
	}
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.Kind, &r.Ratio); err != nil {
			return fmt.Errorf("scan trace completeness ratio row: %w", err)
		}
		got = append(got, r)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate trace completeness ratio rows: %w", err)
	}

	// Only overwrite gauges for kinds that returned from the
	// query. Kinds absent from the result keep their 1.0
	// default (vacuous truth).
	for _, r := range got {
		// Strip the "operator.action." prefix so the metric
		// label matches auditKindClosedSet (the column
		// stores the full kind string).
		const prefix = "operator.action."
		if len(r.Kind) <= len(prefix) || r.Kind[:len(prefix)] != prefix {
			continue
		}
		shortKind := r.Kind[len(prefix):]
		if _, ok := ratios[shortKind]; !ok {
			// Unknown kind (e.g. a future operator.action.<verb>
			// that hasn't been added to the closed set) —
			// skip; do not blow up cardinality.
			continue
		}
		ratios[shortKind] = r.Ratio
	}

	// Push to gauge. nil-safe via the accessor.
	for k, v := range ratios {
		l.ops.SetOperatorActionTraceCompleteness(k, v)
		*gaugeUpdates++
	}
	return nil
}

// sqlStateOf probes the wrapped error chain for a *pgconn.PgError
// and returns its SQLSTATE. Returns "" when the error is not a
// pgx/Postgres error — callers should treat "" as "no
// recognised SQLSTATE" rather than matching on a specific
// code. Mirrors the same probe pattern used at pkg/audit/
// audit.go::errorClassFromErr (PR-#TBD / C5): the audit
// package has its own copy because pkg/audit is widely
// imported and pulling in pgx would reverse the dep
// direction; pkg/sched already imports pgx for the safety
// tick's reclaim so we duplicate the helper here.
//
// The probe uses an interface match rather than importing
// pgconn directly — keeps the helper testable without
// forcing test files to thread pgx types. errors.As walks
// the wrap chain and matches any value exposing
// SQLState() string.
func sqlStateOf(err error) string {
	if err == nil {
		return ""
	}
	type sqlStater interface{ SQLState() string }
	var s sqlStater
	if errors.As(err, &s) {
		return s.SQLState()
	}
	return ""
}
