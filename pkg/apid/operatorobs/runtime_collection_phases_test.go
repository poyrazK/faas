package operatorobs

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/promql"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
)

type runtimePhaseRegistry func(context.Context) ([]state.ComputeNode, error)

func (f runtimePhaseRegistry) ListComputeNodes(ctx context.Context, _ bool) ([]state.ComputeNode, error) {
	return f(ctx)
}

type runtimePhaseDoer func(*http.Request) (*http.Response, error)

func (f runtimePhaseDoer) Do(r *http.Request) (*http.Response, error) { return f(r) }

func TestRuntimeDiagnosticsMetricsPhaseTiming(t *testing.T) {
	for _, tc := range []struct{ phase, slowQuery string }{
		{runtimePhaseInventory, ""},
		{runtimePhaseCoreEvidence, `up{job="apid"}`},
		{runtimePhaseAdvisoryReads, `apid_loop_stalled{job="apid",loop="runtime"}`},
	} {
		t.Run(tc.phase, func(t *testing.T) {
			const delay = 30 * time.Millisecond
			now := time.Now().UTC().Truncate(time.Second)
			fixture, selected, baseRegistry := runtimeFixture("control-plane-v2", now, 1)
			addLoopFixture(fixture, selected.role, now)
			selected.diagnostics, _ = ParseRuntimeDiagnostics(selected.role, "none")
			registry := runtimePhaseRegistry(func(ctx context.Context) ([]state.ComputeNode, error) {
				if tc.phase == runtimePhaseInventory {
					time.Sleep(delay)
				}
				return baseRegistry.ListComputeNodes(ctx, false)
			})
			doer := runtimePhaseDoer(func(r *http.Request) (*http.Response, error) {
				if r.URL.Query().Get("query") == tc.slowQuery {
					time.Sleep(delay)
				}
				return http.DefaultClient.Do(r)
			})
			client := promql.NewClient(inputFixtureClient(t, fixture, now).BaseURL(), doer)
			metrics := NewRuntimeDiagnosticsMetrics(prometheus.NewPedanticRegistry(), nil)
			metrics.RuntimeBuilds(t.Context(), client, registry, selected, now)

			var phaseTotal float64
			for _, phase := range []string{runtimePhaseInventory, runtimePhaseCoreEvidence, runtimePhaseAdvisoryReads, runtimePhaseAssembly, runtimePhasePreview} {
				hist := runtimePhaseHistogram(t, metrics, phase)
				if hist.GetSampleCount() != 1 {
					t.Fatalf("%s observations=%d, want one per collection", phase, hist.GetSampleCount())
				}
				if phase == tc.phase && hist.GetSampleSum() < delay.Seconds() {
					t.Fatalf("%s elapsed=%.6fs, missing injected delay %s", phase, hist.GetSampleSum(), delay)
				}
				phaseTotal += hist.GetSampleSum()
			}
			var total dto.Metric
			if err := metrics.duration.Write(&total); err != nil {
				t.Fatal(err)
			}
			if phaseTotal > total.GetHistogram().GetSampleSum() {
				t.Fatal("overlapping phase timings counted wall time twice")
			}
		})
	}
}

func TestRuntimeDiagnosticsMetricsInventoryOutcomes(t *testing.T) {
	now := time.Now().UTC()
	_, selected, _ := runtimeFixture("control-plane-v1", now, 0)
	gateway := "http://private-host"
	for _, outcome := range []string{"success", "invalid_inventory", "source_error", "deadline_exceeded", "canceled", "not_configured"} {
		t.Run(outcome, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var registry RuntimeBuildRegistry = runtimePhaseRegistry(func(ctx context.Context) ([]state.ComputeNode, error) {
				switch outcome {
				case "invalid_inventory":
					return []state.ComputeNode{{Active: true, GatewayTargetURL: &gateway}}, nil
				case "source_error":
					return nil, errors.New("private-upstream-secret")
				case "deadline_exceeded":
					<-ctx.Done()
					return nil, errors.New("private-upstream-secret")
				case "canceled":
					cancel()
				}
				return nil, nil
			})
			if outcome == "not_configured" {
				registry = nil
			}
			reg := prometheus.NewPedanticRegistry()
			metrics := NewRuntimeDiagnosticsMetrics(reg, nil)
			if outcome == "deadline_exceeded" {
				var stop context.CancelFunc
				ctx, stop = context.WithTimeoutCause(ctx, 30*time.Millisecond, errors.New("private-upstream-secret"))
				defer stop()
			}
			metrics.RuntimeBuilds(ctx, nil, registry, selected, now)
			if n := testutil.ToFloat64(metrics.inventory.WithLabelValues(outcome)); n != 1 {
				t.Fatalf("inventory %s=%v, want 1", outcome, n)
			}
			if h := runtimePhaseHistogram(t, metrics, runtimePhaseInventory); h.GetSampleCount() != 1 {
				t.Fatal("inventory assessment did not emit one elapsed sample")
			}
			if outcome == "deadline_exceeded" {
				assertRuntimeDeadline(t, reg, metrics, runtimePhaseInventory, runtimeDeadlineCaller)
			} else if runtimeDeadlineCount(t, reg) != 0 {
				t.Fatal("inventory assessment without a deadline emitted a deadline observation")
			}
			assertRuntimeMetricsPrivate(t, reg)
		})
	}
}

func TestRuntimeDiagnosticsMetricsCollectionBudgetCountsOnce(t *testing.T) {
	now := time.Now().UTC()
	_, selected, _ := runtimeFixture("single-box-v1", now, 0)
	client := promql.NewClient("http://private-host", runtimePhaseDoer(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	}))
	reg := prometheus.NewPedanticRegistry()
	metrics := NewRuntimeDiagnosticsMetrics(reg, nil)
	metrics.RuntimeBuilds(t.Context(), client, nil, selected, now)
	if n := testutil.ToFloat64(metrics.queries.WithLabelValues("identity", "deadline_exceeded")); n < 2 {
		t.Fatalf("expected several timed-out source reads, got %v", n)
	}
	assertRuntimeDeadline(t, reg, metrics, runtimePhaseCoreEvidence, runtimeDeadlineBudget)
}

func TestRuntimeDiagnosticsMetricsAlreadyExpiredCaller(t *testing.T) {
	now := time.Now().UTC()
	_, selected, _ := runtimeFixture("single-box-v1", now, 0)
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	reg := prometheus.NewPedanticRegistry()
	metrics := NewRuntimeDiagnosticsMetrics(reg, nil)
	metrics.RuntimeBuilds(ctx, nil, nil, selected, now)
	assertRuntimeDeadline(t, reg, metrics, runtimePhaseBefore, runtimeDeadlineCaller)
}

func TestRuntimeDiagnosticsMetricsDoesNotBlamePhaseStartedAfterExpiry(t *testing.T) {
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	reg := prometheus.NewPedanticRegistry()
	metrics := NewRuntimeDiagnosticsMetrics(reg, nil)
	var phase string
	observation := runtimeQueryObservation{metrics: metrics, deadlinePhase: &phase}
	ctx = context.WithValue(ctx, runtimeQueryObservationKey{}, observation)
	observation.observePhase(ctx, runtimePhaseAssembly, time.Now())
	metrics.recordReport(ctx, RuntimeBuildsLoopResponse{}, time.Now())
	assertRuntimeDeadline(t, reg, metrics, runtimePhaseOutside, runtimeDeadlineCaller)
}

func assertRuntimeDeadline(t *testing.T, reg *prometheus.Registry, metrics *RuntimeDiagnosticsMetrics, phase, origin string) {
	t.Helper()
	if n := testutil.ToFloat64(metrics.reports.WithLabelValues("deadline_exceeded")); n != 1 {
		t.Fatalf("deadline reports=%v, want 1", n)
	}
	if n := testutil.ToFloat64(metrics.deadlines.WithLabelValues(phase, origin)); n != 1 {
		t.Fatalf("deadline observations for %s/%s=%v, want 1", phase, origin, n)
	}
	if n := runtimeDeadlineCount(t, reg); n != 1 {
		t.Fatalf("deadline count across all phases and origins=%v, want 1", n)
	}
}

func runtimeDeadlineCount(t *testing.T, reg *prometheus.Registry) float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != "apid_operator_runtime_collection_deadlines_total" {
			continue
		}
		var count float64
		for _, metric := range family.Metric {
			count += metric.GetCounter().GetValue()
		}
		return count
	}
	t.Fatal("collection deadline metric was not registered")
	return 0
}

func runtimePhaseHistogram(t *testing.T, metrics *RuntimeDiagnosticsMetrics, phase string) *dto.Histogram {
	t.Helper()
	var metric dto.Metric
	if err := metrics.phaseDuration.WithLabelValues(phase).(prometheus.Histogram).Write(&metric); err != nil {
		t.Fatal(err)
	}
	return metric.GetHistogram()
}
