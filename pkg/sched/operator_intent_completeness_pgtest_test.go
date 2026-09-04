// pkg/sched/operator_intent_completeness_pgtest_test.go —
// Postgres-backed smoke test for the operatorIntentCompleteness
// tick. Pins the column-ref fix end-to-end:
//
//   - Pre-fix, observeTraceCompletenessRatio's SELECT referenced
//     `received_at`, which does NOT exist on the events table
//     (events.at is the canonical timestamp per
//     migrations/00001_init.sql:132). The query raised
//     "column does not exist", the body's Warn log path
//     swallowed it, and the gauge stayed at the
//     pre-instantiation default (0). The obs/health endpoint
//     would have read 0.0 trace_id coverage on a cluster that
//     had zero data — but, more insidiously, 1.0 on a cluster
//     where the gauge was last stamped pre-bug. The silent
//     failure slipped through every review.
//   - Post-fix, the SELECT reads `at`. The smoke test inserts
//     4 events rows (2 with trace_id, 2 without) and asserts
//     the gauge for "force_park" (the kind the audit emitter
//     stamps on operator.action.park_instance, after the
//     prefix-strip in observeTraceCompletenessRatio) hits 0.5.
//
// Uses pgtest.Open so the test skips via the standard
// FAAS_SKIP_PG_TESTS convention when Postgres is unreachable —
// no `make test` regression in environments without a cluster.
//
// Name (TestRunOperatorIntentCompletenessTick_FixesColumnRef) is
// intentionally explicit so a future grep for "received_at"
// surfaces the regression directly. Same shape as
// pkg/sched/operator_intent_completeness_test.go (nil-ops unit
// coverage); this is the pgtest complement.
package sched

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/wire"
)

// TestRunOperatorIntentCompletenessTick_FixesColumnRef inserts a
// mixed-coverage batch into events and verifies the per-kind
// gauge reports the right ratio. Pre-fix the SQL failed with
// "column received_at does not exist"; this test would surface
// that as a missing label in the metrics body (the gauge would
// be unset for force_park because the row processing skipped
// the body on query error).
func TestRunOperatorIntentCompletenessTick_FixesColumnRef(t *testing.T) {
	pool := pgtest.Open(t)
	defer pool.Close()
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("MigrateUp: %v", err)
	}

	const validTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	// Insert 4 events rows: 2 with trace_id, 2 without. Kind is
	// the post-stamp value — the audit emitter writes the raw
	// "operator.action.park_instance" kind. The Loop's stripping
	// logic (operator_intent_completeness.go around lines
	// 199-213) trims the "operator.action." prefix to map onto
	// the closed-set label "force_park".
	insertEvent := func(traceIDArg interface{}) {
		var err error
		if traceIDArg == nil {
			_, err = pool.Exec(ctx,
				`INSERT INTO events (actor, kind, subject, data) VALUES ($1, $2, NULL, '{}'::jsonb)`,
				"test:schedd", "operator.action.park_instance")
		} else {
			_, err = pool.Exec(ctx,
				`INSERT INTO events (actor, kind, subject, data, trace_id) VALUES ($1, $2, NULL, '{}'::jsonb, $3)`,
				"test:schedd", "operator.action.park_instance", traceIDArg)
		}
		if err != nil {
			t.Fatalf("insert events row (trace_id=%v): %v", traceIDArg, err)
		}
	}
	insertEvent(validTraceID)
	insertEvent(validTraceID)
	insertEvent(nil)
	insertEvent(nil)

	ops := wire.NewOpsMetrics("schedd")
	loop := &Loop{pool: pool, ops: ops, log: silenceLog()}

	gaugeUpdates := loop.observeOperatorIntentCompleteness(ctx)
	if gaugeUpdates == 0 {
		t.Fatalf("observeOperatorIntentCompleteness returned 0 gauge updates; SQL likely failed (column-ref bug?)")
	}

	// Read the per-kind gauge via the metrics body scrape.
	// Pattern mirrors TestObserveOperatorIntentCompleteness_WiredOpsGaugeUpdate
	// (operator_intent_completeness_test.go:115-118). The
	// pre-instantiation grid materialises "force_park" at boot;
	// the Set(ratio) call here replaces the 0 default.
	body := getMetricsBody(t, ops)
	// 2 of 4 rows carry trace_id ⇒ ratio 0.5. Prometheus
	// formats float64 as "0.5" (no trailing zeros).
	wantForcePark := `schedd_operator_action_trace_completeness_ratio{kind="force_park"} 0.5`
	if !strings.Contains(body, wantForcePark) {
		t.Errorf("force_park gauge not stamped to 0.5 (column-ref regression?):\nmetrics body did not contain %q", wantForcePark)
	}
	if !strings.Contains(body, "schedd_operator_action_trace_completeness_first_tick_completed_total 1") {
		t.Errorf("first-tick completion counter was not recorded:\n%s", body)
	}
	if !strings.Contains(body, "# HELP schedd_operator_action_trace_completeness_last_success_timestamp_seconds") {
		t.Errorf("last-success timestamp metric was not registered:\n%s", body)
	}

	// Sanity-pin: the OTHER kinds (force_cold_boot, etc.) stay
	// at their 1.0 vacuous default — no rows in window ⇒
	// pre-instantiated gauge untouched by the kind-grouped
	// query, but the in-memory `ratios` map defaults to 1.0 and
	// the gauge Set() writes 1.0 for those. This is what
	// /obs/health surfaces for "no traffic" kinds.
	for _, k := range []string{"force_cold_boot", "force_restart", "force_park.outcome", "force_cold_boot.outcome", "force_restart.outcome"} {
		want := `schedd_operator_action_trace_completeness_ratio{kind="` + k + `"} 1`
		if !strings.Contains(body, want) {
			t.Errorf("gauge %q not at vacuous default 1.0:\nmetrics body did not contain %q", k, want)
		}
	}
}
