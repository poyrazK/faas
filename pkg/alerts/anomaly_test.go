package alerts_test

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/alerts"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhookout"
)

// anomalyProm answers the coverage and eligible-count query before the
// metric query. Days beyond trafficDays have no app counters.
type anomalyProm struct {
	currentSamples, currentValue float64
	pastSamples, pastValue       float64
	trafficDays                  int
	exactQueries                 atomic.Int32
	lastCheckAt                  atomic.Int64
	currentCount                 func(time.Time) float64
	pastCount                    func(time.Time) float64
	warm                         func(context.Context) error
}

func (p *anomalyProm) QueryScalar(context.Context, string) (float64, error) {
	return 0, errors.New("anomaly query must use an explicit timestamp")
}

func (p *anomalyProm) QueryVectorStrictAt(_ context.Context, query string, at time.Time) ([]promql.VectorSample, error) {
	p.lastCheckAt.Store(at.Unix())
	value := p.currentValue
	if strings.Contains(query, "count_over_time(") {
		value = p.currentSamples
		if p.currentCount != nil {
			value = p.currentCount(at)
		}
	}
	return []promql.VectorSample{{Value: value}}, nil
}

func (p *anomalyProm) QueryRangeStrict(ctx context.Context, q, start, end, step string) ([]promql.QueryRangeSeries, error) {
	first, firstErr := strconv.ParseInt(start, 10, 64)
	last, lastErr := strconv.ParseInt(end, 10, 64)
	const day = int64(24 * time.Hour / time.Second)
	var interval int64
	switch step {
	case "1d":
		p.exactQueries.Add(1)
		interval = day
	case "60":
		if p.warm != nil {
			if err := p.warm(ctx); err != nil {
				return nil, err
			}
		}
		interval = 60
	}
	if firstErr != nil || lastErr != nil || interval == 0 || last < first || (last-first)%interval != 0 {
		return nil, errors.New("history query must use aligned windows")
	}
	isSamples := strings.Contains(q, "count_over_time(")
	rows := make([]promql.QueryRangeSample, (last-first)/interval+1)
	for i := range rows {
		at := first + int64(i)*interval
		value := p.pastValue
		if isSamples {
			value = p.pastSamples
			if p.pastCount != nil {
				value = p.pastCount(time.Unix(at, 0).UTC())
			}
			daysAgo := (p.lastCheckAt.Load() - at + day - 1) / day
			if daysAgo > int64(p.trafficDays) {
				value = -1 // no coverage that day
			}
		}
		rows[i] = promql.QueryRangeSample{Timestamp: at, Value: value}
	}
	return []promql.QueryRangeSeries{{Values: rows}}, nil
}

// TestAnomalyRuleFiresAboveBaseline is the capability acceptance test
// (pkg/productcap/catalog.json, ADR-744).
func TestAnomalyRuleFiresAboveBaseline(t *testing.T) {
	store := state.NewMemStore()
	rule, ident, _ := seedRule(t, store, state.AlertMetricErrorRate, state.AlertAboveBaseline, 3)
	prom := &anomalyProm{currentSamples: 400, currentValue: 5, pastSamples: 400, pastValue: 1, trafficDays: 7}
	dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200, Attempts: 1}}
	ev, _ := makeEvaluator(t, store, prom, ident, dispatch)

	stats, err := ev.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if stats.Fired != 1 || dispatch.callCount() != 1 {
		t.Fatalf("stats=%+v calls=%d, want one fire for 5%% against a usual 1%% at 3x", stats, dispatch.callCount())
	}
	payload := dispatch.calls[0].Payload
	if payload["baseline"] != 1.0 || payload["baseline_days"] != 7 || payload["ratio"] != 5.0 {
		t.Fatalf("payload baseline fields = %v/%v/%v", payload["baseline"], payload["baseline_days"], payload["ratio"])
	}
	got, _ := store.AlertRuleByID(context.Background(), rule.ID)
	if got.State != state.AlertStateFiring {
		t.Fatalf("state = %q, want firing", got.State)
	}

	// The direct read uses one range for coverage and one for error rate.
	// A retry at the same time reuses the historical checkpoint.
	before := prom.exactQueries.Load()
	if before != 2 {
		t.Fatalf("exact historical queries=%d, want 2", before)
	}
	if _, err := ev.RunOnce(context.Background()); err != nil {
		t.Fatalf("second RunOnce: %v", err)
	}
	if after := prom.exactQueries.Load(); after != before {
		t.Fatalf("baseline recomputed at the same timestamp: %d -> %d range queries", before, after)
	}
}

func TestAnomalyRuleStaysQuietOnTinyNumbers(t *testing.T) {
	for _, tc := range []struct {
		name string
		prom *anomalyProm
	}{
		// 0.5% is 5x a 0.1% baseline but below the 1% error-rate floor.
		{"below absolute floor", &anomalyProm{currentSamples: 400, currentValue: 0.5, pastSamples: 400, pastValue: 0.1, trafficDays: 7}},
		{"too few eligible requests", &anomalyProm{currentSamples: 1, currentValue: 100, pastSamples: 400, pastValue: 1, trafficDays: 7}},
		// A zero baseline never makes "x times usual" fire.
		{"zero baseline", &anomalyProm{currentSamples: 400, currentValue: 5, pastSamples: 400, pastValue: 0, trafficDays: 7}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := state.NewMemStore()
			_, ident, _ := seedRule(t, store, state.AlertMetricErrorRate, state.AlertAboveBaseline, 3)
			dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200}}
			ev, _ := makeEvaluator(t, store, tc.prom, ident, dispatch)
			stats, err := ev.RunOnce(context.Background())
			if err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			if stats.Fired != 0 || dispatch.callCount() != 0 {
				t.Fatalf("stats=%+v: must not fire", stats)
			}
		})
	}
}

func TestAnomalyRuleExplainsCompletedQuietEvaluations(t *testing.T) {
	for _, tc := range []struct {
		name       string
		metric     state.AlertMetric
		comparison state.AlertComparison
		threshold  float64
		prom       *anomalyProm
		want       api.AlertAnomalyEvaluation
	}{
		{
			name: "increase below the baseline threshold", metric: state.AlertMetricErrorRate, comparison: state.AlertAboveBaseline, threshold: 3,
			prom: &anomalyProm{currentSamples: 400, currentValue: 1.5, pastSamples: 400, pastValue: 0.75, trafficDays: 7},
			want: api.AlertAnomalyEvaluation{Reason: api.AlertAnomalyThresholdNotReached, Observed: new(1.5), Baseline: new(0.75), TriggerValue: new(2.25)},
		},
		{
			name: "increase above the baseline threshold but below the observation minimum", metric: state.AlertMetricLatencyP95, comparison: state.AlertAboveBaseline, threshold: 3,
			prom: &anomalyProm{currentSamples: 400, currentValue: 30, pastSamples: 400, pastValue: 10, trafficDays: 7},
			want: api.AlertAnomalyEvaluation{Reason: api.AlertAnomalyMinimumNotReached, Observed: new(30.0), Baseline: new(10.0), TriggerValue: new(30.0), MinimumValue: new(50.0)},
		},
		{
			name: "increase from zero baseline", metric: state.AlertMetricErrorRate, comparison: state.AlertAboveBaseline, threshold: 3,
			prom: &anomalyProm{currentSamples: 400, currentValue: 5, pastSamples: 400, pastValue: 0, trafficDays: 7},
			want: api.AlertAnomalyEvaluation{Reason: api.AlertAnomalyZeroBaseline, Observed: new(5.0), Baseline: new(0.0)},
		},
		{
			name: "traffic drop above the baseline threshold", metric: state.AlertMetricRequestCount, comparison: state.AlertBelowBaseline, threshold: 0.25,
			prom: &anomalyProm{currentSamples: 80, pastSamples: 200, trafficDays: 7},
			want: api.AlertAnomalyEvaluation{Reason: api.AlertAnomalyThresholdNotReached, Observed: new(80.0), Baseline: new(200.0), TriggerValue: new(50.0)},
		},
		{
			name: "traffic drop below the baseline minimum with zero observed", metric: state.AlertMetricRequestCount, comparison: state.AlertBelowBaseline, threshold: 0.25,
			prom: &anomalyProm{currentSamples: 0, pastSamples: 40, trafficDays: 7},
			want: api.AlertAnomalyEvaluation{Reason: api.AlertAnomalyMinimumNotReached, Observed: new(0.0), Baseline: new(40.0), TriggerValue: new(10.0), MinimumValue: new(50.0)},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := state.NewMemStore()
			rule, ident, _ := seedRule(t, store, tc.metric, tc.comparison, tc.threshold)
			dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200}}
			ev, _ := makeEvaluator(t, store, tc.prom, ident, dispatch)
			stats, err := ev.RunOnce(t.Context())
			if err != nil || stats.Fired != 0 || stats.SkippedInsufficient != 0 || stats.SkippedDegraded != 0 || dispatch.callCount() != 0 {
				t.Fatalf("stats=%+v err=%v, want a completed quiet check", stats, err)
			}
			got, err := store.AlertRuleByID(t.Context(), rule.ID)
			want := tc.want
			want.EvaluatedAt = got.LastEvaluatedAt
			if err != nil || got.State != state.AlertStateOk || want.EvaluatedAt.IsZero() || !reflect.DeepEqual(got.AnomalyEvaluation, want) {
				t.Fatalf("state=%q evidence=%+v err=%v, want ok with %+v", got.State, got.AnomalyEvaluation, err, want)
			}
		})
	}
}

func TestAnomalyRuleNeedsEnoughHistory(t *testing.T) {
	store := state.NewMemStore()
	rule, ident, _ := seedRule(t, store, state.AlertMetricErrorRate, state.AlertAboveBaseline, 3)
	prom := &anomalyProm{currentSamples: 400, currentValue: 50, pastSamples: 400, pastValue: 1, trafficDays: 3}
	dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200}}
	ev, _ := makeEvaluator(t, store, prom, ident, dispatch)
	stats, err := ev.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if stats.Fired != 0 || stats.SkippedInsufficient != 1 {
		t.Fatalf("stats=%+v, want skipped as insufficient with 3 of 7 days", stats)
	}
	got, err := store.AlertRuleByID(t.Context(), rule.ID)
	if err != nil || got.AnomalyEvaluation.Reason != api.AlertAnomalyInsufficientHistory ||
		got.AnomalyEvaluation.Count != 3 || got.AnomalyEvaluation.RequiredCount != 5 {
		t.Fatalf("evidence=%+v err=%v, want 3 of 5 qualifying windows", got.AnomalyEvaluation, err)
	}
}

func TestAnomalyRuleRejectsSparseHistoricalPopulation(t *testing.T) {
	store := state.NewMemStore()
	_, ident, _ := seedRule(t, store, state.AlertMetricErrorRate, state.AlertAboveBaseline, 3)
	prom := &anomalyProm{currentSamples: 400, currentValue: 50, pastSamples: 49, pastValue: 1, trafficDays: 7}
	dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200}}
	ev, _ := makeEvaluator(t, store, prom, ident, dispatch)
	stats, err := ev.RunOnce(t.Context())
	if err != nil || stats.Fired != 0 || stats.SkippedInsufficient != 1 {
		t.Fatalf("stats=%+v err=%v, want insufficient historical samples", stats, err)
	}
}

func TestAnomalyRuleBelowBaselineCatchesTrafficDrop(t *testing.T) {
	for _, tc := range []struct {
		name      string
		count     float64
		wantState state.AlertState
		wantFires int
	}{
		{"drop", 100, state.AlertStateFiring, 1},
		{"confirmed zero", 0, state.AlertStateFiring, 1},
		{"missing or stale telemetry", -1, state.AlertStateDegraded, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := state.NewMemStore()
			rule, ident, _ := seedRule(t, store, state.AlertMetricRequestCount, state.AlertBelowBaseline, 0.3)
			prom := &anomalyProm{currentSamples: tc.count, pastSamples: 1000, trafficDays: 7}
			dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200, Attempts: 1}}
			ev, _ := makeEvaluator(t, store, prom, ident, dispatch)
			stats, err := ev.RunOnce(t.Context())
			if err != nil || stats.Fired != tc.wantFires || dispatch.callCount() != tc.wantFires {
				t.Fatalf("stats=%+v deliveries=%d err=%v, want %d fires", stats, dispatch.callCount(), err, tc.wantFires)
			}
			got, err := store.AlertRuleByID(t.Context(), rule.ID)
			if err != nil || got.State != tc.wantState {
				t.Fatalf("state=%q err=%v, want %q", got.State, err, tc.wantState)
			}
			if tc.wantState == state.AlertStateDegraded && got.AnomalyEvaluation.Reason != api.AlertAnomalyTelemetryMissing {
				t.Fatalf("evidence=%+v, want missing request telemetry", got.AnomalyEvaluation)
			}
		})
	}
}

func TestAnomalyRuleAlignsBaselineAfterTrafficRamp(t *testing.T) {
	store := state.NewMemStore()
	rule, ident, _ := seedRule(t, store, state.AlertMetricRequestCount, state.AlertAboveBaseline, 3)
	morning := time.Date(2026, 7, 28, 9, 0, 0, 0, time.UTC)
	now := morning
	prom := &anomalyProm{
		trafficDays: 7,
		currentCount: func(at time.Time) float64 {
			switch at.Minute() {
			case 15:
				return 400
			case 16:
				return 1500
			default:
				return 100
			}
		},
		pastCount: func(at time.Time) float64 {
			if at.Minute() >= 15 {
				return 400
			}
			return 100
		},
	}
	dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200}}
	ev := alerts.NewEvaluator(alerts.EvaluatorOptions{
		Store: store, PromQL: prom, Dispatcher: dispatch, Log: discardLog(),
		Identity: func() *age.X25519Identity { return ident },
		Now:      func() time.Time { return now },
	})
	for _, tc := range []struct {
		minute, fires int
		reason        api.AlertAnomalyEvaluationReason
	}{{0, 0, api.AlertAnomalyThresholdNotReached}, {15, 0, api.AlertAnomalyThresholdNotReached}, {16, 1, ""}} {
		now = morning.Add(time.Duration(tc.minute) * time.Minute)
		stats, err := ev.RunOnce(t.Context())
		if err != nil || stats.Fired != tc.fires || stats.SkippedDegraded != 0 {
			t.Fatalf("%v: stats=%+v err=%v, want %d fires", now, stats, err, tc.fires)
		}
		got, err := store.AlertRuleByID(t.Context(), rule.ID)
		if err != nil || got.AnomalyEvaluation.Reason != tc.reason {
			t.Fatalf("%v: evidence=%+v err=%v, want %q", now, got.AnomalyEvaluation, err, tc.reason)
		}
	}
	if dispatch.callCount() != 1 || dispatch.calls[0].Payload["baseline"] != 400.0 {
		t.Fatalf("deliveries=%+v, want one fire with the 09:16 historical baseline", dispatch.calls)
	}
}

func TestAnomalyRuleReportsRepeatNotificationCooldown(t *testing.T) {
	store := state.NewMemStore()
	rule, ident, _ := seedRule(t, store, state.AlertMetricErrorRate, state.AlertAboveBaseline, 3)
	start := rule.UpdatedAt.UTC().Truncate(time.Second).Add(time.Minute)
	interval := time.Duration(rule.CooldownMinutes) * time.Minute
	now := start
	prom := &anomalyProm{pastSamples: 400, pastValue: 1, trafficDays: 7}
	dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200}}
	ev := alerts.NewEvaluator(alerts.EvaluatorOptions{
		Store: store, PromQL: prom, Dispatcher: dispatch, Log: discardLog(),
		Identity: func() *age.X25519Identity { return ident },
		Now:      func() time.Time { return now },
	})
	const cooldown = api.AlertAnomalyNotificationCooldown
	fires := 0
	for _, step := range []struct {
		name           string
		after          time.Duration
		samples, value float64
		state          state.AlertState
		reason         api.AlertAnomalyEvaluationReason
		fire           int
	}{
		{"first fire", 0, 400, 5, state.AlertStateFiring, "", 1},
		{"same incident during cooldown", time.Minute, 400, 5, state.AlertStateFiring, cooldown, 0},
		{"telemetry missing during the incident", 2 * time.Minute, -1, 5, state.AlertStateDegraded, api.AlertAnomalyTelemetryMissing, 0},
		{"breach resumes after missing telemetry", 3 * time.Minute, 400, 5, state.AlertStateFiring, cooldown, 0},
		{"repeat exactly at the deadline", interval, 400, 5, state.AlertStateFiring, "", 1},
		{"repeat starts a new cooldown", interval + time.Minute, 400, 5, state.AlertStateFiring, cooldown, 0},
		{"incident recovers", interval + 2*time.Minute, 400, 2, state.AlertStateOk, api.AlertAnomalyThresholdNotReached, 0},
		{"fresh incident inside old cooldown", interval + 3*time.Minute, 400, 5, state.AlertStateFiring, "", 1},
	} {
		now = start.Add(step.after)
		prom.currentSamples, prom.currentValue = step.samples, step.value
		stats, err := ev.RunOnce(t.Context())
		fires += step.fire
		if err != nil || stats.Fired != step.fire || dispatch.callCount() != fires {
			t.Fatalf("%s: stats=%+v deliveries=%d err=%v, want %d new fires", step.name, stats, dispatch.callCount(), err, step.fire)
		}
		row, err := store.AlertRuleByID(t.Context(), rule.ID)
		evidence := row.AnomalyEvaluation
		if err != nil || row.State != step.state || evidence.Reason != step.reason || !row.LastEvaluatedAt.Equal(now) {
			t.Fatalf("%s: state=%q evidence=%+v err=%v, want %q with %q", step.name, row.State, evidence, err, step.state, step.reason)
		}
		if step.reason == cooldown {
			want := api.AlertAnomalyEvaluation{
				Reason: cooldown, EvaluatedAt: now, Observed: new(5.0), Baseline: new(1.0), TriggerValue: new(3.0),
				NextNotificationEligibleAt: new(row.LastFiredAt.Add(interval)),
			}
			if !reflect.DeepEqual(evidence, want) {
				t.Fatalf("%s: evidence=%+v, want %+v", step.name, evidence, want)
			}
		} else if evidence.NextNotificationEligibleAt != nil {
			t.Fatalf("%s: retained old cooldown evidence", step.name)
		}
	}
}

func TestAnomalyRulesShareMatchingBaseline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := state.NewMemStore()
		rule, ident, _ := seedRule(t, store, state.AlertMetricRequestCount, state.AlertAboveBaseline, 3)
		rule.ID, rule.Name = "", "second-anomaly-rule"
		rule.Threshold = 2
		if _, err := store.CreateAlertRule(t.Context(), rule); err != nil {
			t.Fatal(err)
		}
		warming := make(chan struct{})
		prom := &anomalyProm{
			currentSamples: 400, pastSamples: 100, trafficDays: 7,
			warm: func(ctx context.Context) error {
				select {
				case <-warming:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			},
		}
		dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200}}
		start := rule.UpdatedAt.Add(time.Minute).Truncate(time.Second)
		now := start.Add(time.Second)
		ev := alerts.NewEvaluator(alerts.EvaluatorOptions{
			Store: store, PromQL: prom, Dispatcher: dispatch, Log: discardLog(),
			Identity: func() *age.X25519Identity { return ident },
			Now:      func() time.Time { return now },
		})
		stats, err := ev.RunOnceAt(t.Context(), start)
		if err != nil || stats.Fired != 2 || prom.exactQueries.Load() != 1 {
			t.Errorf("stats=%+v exact queries=%d err=%v, want two fires from one shared read while warming is blocked", stats, prom.exactQueries.Load(), err)
		}
		close(warming)
		synctest.Wait()
		scheduled := start.Add(time.Minute)
		now = scheduled.Add(3 * time.Second)
		if _, err := ev.RunOnceAt(t.Context(), scheduled); err != nil || prom.exactQueries.Load() != 1 || prom.lastCheckAt.Load() != scheduled.Unix() {
			t.Errorf("delayed tick sampled at %d, ran %d total exact queries, err=%v", prom.lastCheckAt.Load(), prom.exactQueries.Load(), err)
		}
		scheduled = start.Add(api.AnomalyBaselineCacheTTL)
		now = scheduled.Add(time.Second)
		if _, err := ev.RunOnceAt(t.Context(), scheduled); err != nil || prom.exactQueries.Load() != 2 {
			t.Errorf("expired cache ran %d total exact queries, err=%v", prom.exactQueries.Load(), err)
		}
	})
}
