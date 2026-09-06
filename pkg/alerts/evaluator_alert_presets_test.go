// evaluator_alert_presets_test.go — alert-preset evaluator tests
// (issue #1233 / ADR-123 and issue #1395 B3). Pins the durable cases in
// pkg/alerts/evaluator.go::observe:
//
//  1. AlertMetricFailedDeployments — Postgres-backed via
//     store.CountFailedDeploymentsSince (mirrors the
//     failed_invocations case at evaluator_test.go:482).
//  2. AlertMetricAPIUp — Postgres-backed via
//     store.WasInvokedSuccessfullySince; threshold=1 means
//     "reachable" so comparison 'lt 1' fires when no successful
//     invocation landed in the window.
//  3. AlertMetricAccountSpendEUR — Postgres-backed MTD SUM via
//     store.MTDSpendEurCents; the wire-side threshold unit is
//     EUR (catalog's spend_eur_20 fires at gt 20), the store
//     returns cents, the evaluator divides by 100.
//  4. AlertMetricCertExpirySeconds — Postgres-backed via
//     store.MinCertExpiryForApp; the customer-facing preset
//     uses comparison 'lt 1209600' for "fewer than 14 days
//     remaining".
//  5. AlertMetricQueueDepth — PromQL-driven, surfaced on
//     appmetrics.Fetch's QueueDepth field. When prom is nil
//     the source is degraded and the evaluator skips without
//     firing.
//
// The B3 table-driven test below covers all four new alert metrics with
// deterministic store values, while the Postgres-backed reads remain
// covered by the pgstore integration tests when a test database is present.
package alerts_test

import (
	"context"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/alerts"
	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhookout"
)

// b3MetricStore keeps the evaluator test on the real MemStore for alert-rule
// lifecycle and delivery state while supplying deterministic values for the
// three production-only rollup reads introduced by issue #1395 B3.
type b3MetricStore struct {
	*state.MemStore
	newErrors   int
	coldWakePct float64
	dailyCents  int64
}

func (s *b3MetricStore) CountNewErrorFingerprintsSince(context.Context, string, string, time.Time) (int, error) {
	return s.newErrors, nil
}

func (s *b3MetricStore) ColdWakeRatePctSince(context.Context, string, string, time.Time) (float64, error) {
	return s.coldWakePct, nil
}

func (s *b3MetricStore) DailyCostCents(context.Context, string, string, time.Time) (int64, error) {
	return s.dailyCents, nil
}

// TestEvaluator_FailedDeployments mirrors TestEvaluator_FailedInvocations
// for the new deployment_failed metric (issue #1233 / ADR-123).
// seedRule already creates the app + account via the standard
// helpers, so the test only needs to seed deployments. The
// timestamp filter must prune the older deployment so the count
// stays at 2.
func TestEvaluator_FailedDeployments(t *testing.T) {
	store := state.NewMemStore()
	rule, ident, _ := seedRule(t, store, state.AlertMetricFailedDeployments, state.AlertGt, 0)
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	// Two failed deployments inside the 1h window — seedRule's
	// default WindowSpec is 5m, but we explicitly use the 1h
	// catalog default to match the deployment_failed preset.
	for i := 0; i < 2; i++ {
		if _, err := store.CreateDeployment(context.Background(), state.Deployment{
			AppID:     rule.AppID,
			Status:    state.DeployFailed,
			CreatedAt: now.Add(-time.Minute),
		}); err != nil {
			t.Fatalf("CreateDeployment (recent): %v", err)
		}
	}
	// One failed deployment OUTSIDE the 1h window — the
	// timestamp filter must prune it so the count stays at 2.
	if _, err := store.CreateDeployment(context.Background(), state.Deployment{
		AppID:     rule.AppID,
		Status:    state.DeployFailed,
		CreatedAt: now.Add(-2 * time.Hour),
	}); err != nil {
		t.Fatalf("CreateDeployment (old): %v", err)
	}
	dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200, Attempts: 1}}
	ev := alerts.NewEvaluator(alerts.EvaluatorOptions{
		Store:      store,
		PromQL:     nil, // failed_deployments doesn't touch PromQL
		Audit:      audit.New(store, discardLog(), nil, "meterd"),
		Identity:   func() *age.X25519Identity { return ident },
		Dispatcher: dispatch,
		Now:        func() time.Time { return now },
		Log:        discardLog(),
	})
	stats, err := ev.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if stats.Fired != 1 || stats.Delivered != 1 {
		t.Errorf("stats = %+v; want Fired=1, Delivered=1", stats)
	}
	if dispatch.callCount() != 1 {
		t.Errorf("dispatch calls = %d; want 1", dispatch.callCount())
	}
}

// TestEvaluator_APIUp_NoSuccessfulInvocationFires pins the
// catalog's api_down preset contract: when no successful invocation
// has landed in the window,
// WasInvokedSuccessfullySince returns (false, nil), observed=0,
// and the comparison 'lt 1' fires — the API is genuinely down.
func TestEvaluator_APIUp_NoSuccessfulInvocationFires(t *testing.T) {
	store := state.NewMemStore()
	seedRule(t, store, state.AlertMetricAPIUp, state.AlertLt, 1)
	dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200, Attempts: 1}}
	ev := alerts.NewEvaluator(alerts.EvaluatorOptions{
		Store:      store,
		PromQL:     nil, // WasInvokedSuccessfullySince is the Postgres path
		Audit:      audit.New(store, discardLog(), nil, "meterd"),
		Identity:   func() *age.X25519Identity { return nil }, // skip dispatch
		Dispatcher: dispatch,
		Now:        func() time.Time { return time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC) },
		Log:        discardLog(),
	})
	stats, err := ev.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if stats.Fired != 1 {
		t.Errorf("stats.Fired = %d; want 1 (api is down — observed=0 < threshold=1)", stats.Fired)
	}
}

// TestEvaluator_QueueDepth_DegradedSource pins the PromQL-driven
// branch's degraded-source contract: when prom is nil the source
// is degraded and the evaluator MUST NOT fire — the queue depth
// is in an unknown state, not a confirmed "high" state. The
// catalog's queue_backlog_growing preset uses comparison 'gt 50',
// so a degraded source must return skipDegraded rather than
// guessing.
func TestEvaluator_QueueDepth_DegradedSource(t *testing.T) {
	store := state.NewMemStore()
	seedRule(t, store, state.AlertMetricQueueDepth, state.AlertGt, 50)
	dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200, Attempts: 1}}
	ev := alerts.NewEvaluator(alerts.EvaluatorOptions{
		Store:      store,
		PromQL:     nil, // queue_depth is PromQL-driven → degraded source
		Audit:      audit.New(store, discardLog(), nil, "meterd"),
		Identity:   func() *age.X25519Identity { return nil }, // skip dispatch
		Dispatcher: dispatch,
		Now:        func() time.Time { return time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC) },
		Log:        discardLog(),
	})
	stats, err := ev.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if stats.Fired != 0 {
		t.Errorf("stats.Fired = %d; want 0 (PromQL degraded — must not fire)", stats.Fired)
	}
	if dispatch.callCount() != 0 {
		t.Errorf("dispatch calls = %d; want 0 (degraded source must short-circuit)", dispatch.callCount())
	}
}

func TestEvaluator_B3Metrics(t *testing.T) {
	cases := []struct {
		name      string
		metric    state.AlertMetric
		threshold float64
		value     float64
		prom      bool
	}{
		{name: "new error fingerprint", metric: state.AlertMetricNewErrorFingerprint, threshold: 0, value: 2},
		{name: "cold wake rate", metric: state.AlertMetricColdWakeRatePct, threshold: 10, value: 12.5},
		{name: "queue depth", metric: state.AlertMetricQueueDepth, threshold: 50, value: 75, prom: true},
		{name: "daily cost", metric: state.AlertMetricDailyCostCents, threshold: 100, value: 250},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := state.NewMemStore()
			rule, ident, _ := seedRule(t, base, tc.metric, state.AlertGt, tc.threshold)
			store := &b3MetricStore{
				MemStore:    base,
				newErrors:   int(tc.value),
				coldWakePct: tc.value,
				dailyCents:  int64(tc.value),
			}
			dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200, Attempts: 1}}
			var prom appmetrics.PromQL
			if tc.prom {
				prom = &stubPromQL{value: tc.value}
			}
			ev := alerts.NewEvaluator(alerts.EvaluatorOptions{
				Store:      store,
				PromQL:     prom,
				Audit:      audit.New(store, discardLog(), nil, "meterd"),
				Identity:   func() *age.X25519Identity { return ident },
				Dispatcher: dispatch,
				Now:        func() time.Time { return time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC) },
				Log:        discardLog(),
			})
			stats, err := ev.RunOnce(context.Background())
			if err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			if stats.Fired != 1 || stats.Delivered != 1 {
				t.Fatalf("stats = %+v; want Fired=1, Delivered=1", stats)
			}
			if dispatch.callCount() != 1 {
				t.Fatalf("dispatch calls = %d; want 1", dispatch.callCount())
			}
			if rule.Metric != tc.metric {
				t.Fatalf("seeded metric = %q; want %q", rule.Metric, tc.metric)
			}
		})
	}
}
