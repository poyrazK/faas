package operatorobs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
)

type runtimeMetricsCountingDoer struct{ calls atomic.Int64 }

func (d *runtimeMetricsCountingDoer) Do(r *http.Request) (*http.Response, error) {
	d.calls.Add(1)
	return http.DefaultClient.Do(r)
}

func TestRuntimeDiagnosticsMetricsPreserveResponseAndQueries(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	f, selected, registry := runtimeFixture("single-box-v2", now, 1)
	addLoopFixture(f, selected.role, now)
	selected.diagnostics, _ = ParseRuntimeDiagnostics(selected.role, "none")
	doer := &runtimeMetricsCountingDoer{}
	client := promql.NewClient(inputFixtureClient(t, f, now).BaseURL(), doer)
	want := RuntimeBuildsWithLoopHealth(t.Context(), client, registry, selected, now)
	calls := doer.calls.Load()
	reg := prometheus.NewPedanticRegistry()
	m := NewRuntimeDiagnosticsMetrics(reg, nil)
	if stamp := testutil.ToFloat64(m.lastComplete); stamp != 0 {
		t.Fatalf("last complete report timestamp at startup = %v, want 0", stamp)
	}
	if stamp := runtimeLastCollectionTimestamp(t, reg, "not_collected"); stamp != 0 {
		t.Fatalf("last collection timestamp at startup = %v, want 0", stamp)
	}
	started := time.Now()
	got := m.RuntimeBuilds(t.Context(), client, registry, selected, now)
	stamp := testutil.ToFloat64(m.lastComplete)
	if earliest, latest := float64(started.UnixNano())/1e9, float64(time.Now().UnixNano())/1e9; stamp < earliest || stamp > latest {
		t.Fatalf("last complete report timestamp = %v, want between %v and %v", stamp, earliest, latest)
	}
	if collected := runtimeLastCollectionTimestamp(t, reg, "complete"); collected != stamp {
		t.Fatalf("last collection timestamp = %v, want last complete timestamp %v", collected, stamp)
	}
	if h := runtimePhaseHistogram(t, m, runtimePhaseInventory); h.GetSampleCount() != 0 {
		t.Fatal("static profile recorded an inventory phase")
	}
	wantJSON, _ := json.Marshal(want)
	gotJSON, _ := json.Marshal(got)
	if string(wantJSON) != string(gotJSON) || doer.calls.Load() != 2*calls {
		t.Fatal("instrumentation changed response or issued additional queries")
	}
	var disabled *RuntimeDiagnosticsMetrics
	plain := disabled.RuntimeBuilds(t.Context(), client, registry, selected, now)
	plainJSON, _ := json.Marshal(plain)
	if string(plainJSON) != string(wantJSON) {
		t.Fatal("nil metrics changed collection")
	}
	if n := testutil.ToFloat64(m.reports.WithLabelValues("complete")); n != 1 {
		t.Fatalf("complete=%v, response=%s", n, gotJSON)
	}
	assertRuntimeIncompleteGroups(t, m)
	assertRuntimeGroupEvaluations(t, m, []string{"identity", "readiness", "process", "loop_health"}, nil)
	assertRuntimeCoreEvidenceGaps(t, m, nil)
	for _, group := range []string{"identity", "readiness", "process", "loop_health"} {
		if n := testutil.ToFloat64(m.queries.WithLabelValues(group, "success")); n == 0 {
			t.Fatalf("no %s query observations", group)
		}
	}
	if n := testutil.ToFloat64(m.queries.WithLabelValues("builder_health", "success")); n != 0 {
		t.Fatalf("unselected group queried: %v", n)
	}
	var metric dto.Metric
	if err := m.responseBytes.Write(&metric); err != nil {
		t.Fatal(err)
	}
	if metric.GetHistogram().GetSampleCount() != 1 || metric.GetHistogram().GetSampleSum() != float64(len(gotJSON)+1) {
		t.Fatal("incorrect encoded response size")
	}
	assertRuntimeMetricsPrivate(t, reg)
}

func TestRuntimeDiagnosticsMetricsIncompleteReadiness(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	f, selected, registry := runtimeFixture("single-box-v2", now, 1)
	addLoopFixture(f, selected.role, now)
	selected.diagnostics, _ = ParseRuntimeDiagnostics(selected.role, "none")
	ready, _ := readinessQueries(runtimeBuildJobSpecs(selected.role)[0])
	f[ready], f["timestamp("+ready+")"] = nil, nil

	m := NewRuntimeDiagnosticsMetrics(prometheus.NewPedanticRegistry(), nil)
	out := m.RuntimeBuilds(t.Context(), inputFixtureClient(t, f, now), registry, selected, now)
	if out.Readiness == nil || out.Readiness.Counts.Missing != 1 {
		t.Fatalf("expected one missing readiness sample, got %+v", out.Readiness)
	}
	if n := testutil.ToFloat64(m.queries.WithLabelValues("readiness", "success")); n == 0 {
		t.Fatal("fixture did not return successful readiness queries")
	}
	if n := testutil.ToFloat64(m.queries.WithLabelValues("readiness", "source_error")); n != 0 {
		t.Fatalf("fixture produced %v readiness query errors", n)
	}
	if n := testutil.ToFloat64(m.reports.WithLabelValues("incomplete")); n != 1 {
		t.Fatalf("incomplete reports = %v, want 1", n)
	}
	assertRuntimeIncompleteGroups(t, m, "readiness")
	assertRuntimeGroupEvaluations(t, m, []string{"identity", "process", "loop_health"}, []string{"readiness"})
	assertRuntimeCoreEvidenceGaps(t, m, map[string][]string{"readiness": {"missing"}})
}

func TestRuntimeDiagnosticsMetricsAllGroups(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	f, selected, registry := runtimeFixture("control-plane-v2", now, 1)
	addLoopFixture(f, selected.role, now)
	reg := prometheus.NewPedanticRegistry()
	m := NewRuntimeDiagnosticsMetrics(reg, nil)
	m.RuntimeBuilds(t.Context(), inputFixtureClient(t, f, now), registry, selected, now)
	for _, group := range runtimeMetricGroups() {
		if n := testutil.ToFloat64(m.queries.WithLabelValues(group, "success")); n == 0 {
			t.Fatalf("missing instrumentation for %s", group)
		}
		complete := testutil.ToFloat64(m.evaluations.WithLabelValues(group, "complete"))
		incomplete := testutil.ToFloat64(m.evaluations.WithLabelValues(group, "incomplete"))
		if complete+incomplete != 1 {
			t.Errorf("evaluations for %s = %v, want 1", group, complete+incomplete)
		}
	}
	assertRuntimeMetricsPrivate(t, reg)
}

func TestRuntimeDiagnosticsMetricsCollectionOutcomes(t *testing.T) {
	for _, tc := range []struct{ name, report, query, reason string }{
		{"deadline", "deadline_exceeded", "deadline_exceeded", ""},
		{"cancel", "canceled", "canceled", ""},
		{"already_canceled", "canceled", "canceled", ""},
		{"query_deadline", "incomplete", "deadline_exceeded", ""},
		{"transport", "incomplete", "source_error", "transport_error"},
		{"upstream", "incomplete", "source_error", "http_error"},
		{"invalid_json", "incomplete", "source_error", "invalid_response"},
		{"invalid_vector", "incomplete", "source_error", "invalid_response"},
		{"bad_url", "incomplete", "source_error", "other"},
		{"empty", "incomplete", "success", ""},
		{"not_configured", "incomplete", "not_configured", ""},
		{"unselected", "unselected", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Second)
			_, selected, registry := runtimeFixture("single-box-v1", now, 1)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			var requests atomic.Int64
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				switch tc.name {
				case "deadline", "query_deadline":
					<-r.Context().Done()
				case "cancel":
					cancel()
					<-r.Context().Done()
				case "upstream":
					http.Error(w, "private-upstream-secret", http.StatusServiceUnavailable)
				case "invalid_json":
					_, _ = w.Write([]byte("private-upstream-secret"))
				case "invalid_vector":
					_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1,"NaN"]}]}}`))
				default:
					_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
				}
			}))
			defer source.Close()
			client := promql.NewClient(source.URL, source.Client())
			switch tc.name {
			case "query_deadline":
				client = client.WithTimeout(40 * time.Millisecond)
			case "transport":
				client = promql.NewClient(source.URL, runtimePhaseDoer(func(*http.Request) (*http.Response, error) {
					return nil, errors.New("private-upstream-secret")
				}))
			case "bad_url":
				client = promql.NewClient("://broken", source.Client())
			case "not_configured":
				client = nil
			case "unselected":
				selected.role = ""
			}
			reg := prometheus.NewPedanticRegistry()
			m := NewRuntimeDiagnosticsMetrics(reg, nil)
			const lastComplete = 123
			m.lastComplete.Set(lastComplete)
			if tc.name == "deadline" {
				var stop context.CancelFunc
				ctx, stop = context.WithTimeout(ctx, 100*time.Millisecond)
				defer stop()
			}
			if tc.name == "already_canceled" {
				cancel()
			}
			started := time.Now()
			out := m.RuntimeBuilds(ctx, client, registry, selected, now)
			if time.Since(started) > 2*time.Second {
				t.Fatal("source cancellation did not bound collection")
			}
			if n := testutil.ToFloat64(m.reports.WithLabelValues(tc.report)); n != 1 {
				t.Fatalf("report %s=%v", tc.report, n)
			}
			if tc.report != "incomplete" {
				assertRuntimeIncompleteGroups(t, m)
				assertRuntimeGroupEvaluations(t, m, nil, nil)
				assertRuntimeCoreEvidenceGaps(t, m, nil)
			}
			if stamp := testutil.ToFloat64(m.lastComplete); stamp != lastComplete {
				t.Fatalf("%s updated last complete report timestamp to %v", tc.report, stamp)
			}
			if stamp := runtimeLastCollectionTimestamp(t, reg, tc.report); stamp < float64(started.UnixNano())/1e9 {
				t.Fatalf("last collection timestamp = %v, earlier than collection start", stamp)
			}
			if tc.report != "deadline_exceeded" && runtimeDeadlineCount(t, reg) != 0 {
				t.Fatal("a report without a collection deadline incremented its counter")
			}
			if tc.query != "" && testutil.ToFloat64(m.queries.WithLabelValues("identity", tc.query)) == 0 {
				t.Fatalf("missing %s query outcome", tc.query)
			}
			for _, reason := range []string{"transport_error", "http_error", "invalid_response", "other"} {
				var want float64
				if reason == tc.reason {
					want = testutil.ToFloat64(m.queries.WithLabelValues("identity", "source_error"))
				}
				if n := testutil.ToFloat64(m.queryFailures.WithLabelValues("identity", reason)); n != want {
					t.Fatalf("identity failures with reason %s = %v, want %v", reason, n, want)
				}
			}
			if (tc.query == "" || tc.name == "already_canceled") && requests.Load() != 0 {
				t.Fatal("unselected or canceled collection issued HTTP queries")
			}
			if tc.name == "deadline" {
				var metric dto.Metric
				if err := m.duration.Write(&metric); err != nil {
					t.Fatal(err)
				}
				if metric.GetHistogram().GetSampleSum() < 0.08 {
					t.Fatal("collection duration excluded initial identity reads")
				}
			}
			body, _ := json.Marshal(out)
			if strings.Contains(string(body), "private-upstream-secret") {
				t.Fatal("raw source error leaked into response")
			}
			assertRuntimeMetricsPrivate(t, reg)
		})
	}
}

func TestRuntimeDiagnosticsMetricsHealthAndPreviewAccounting(t *testing.T) {
	reg := prometheus.NewPedanticRegistry()
	m := NewRuntimeDiagnosticsMetrics(reg, nil)
	out := fitRuntimeLoopResponse(runtimeEnvelopeFixture())
	// Fresh mismatches are evidence, not a collection failure.
	out.Jobs = []api.ObsRuntimeBuildJob{{Job: "apid", State: "available", Expected: 41}}
	out.Readiness = &api.ObsRuntimeReadiness{State: "not_ready", Counts: api.ObsRuntimeReadinessCounts{NotReady: 41}}
	out.Process = &api.ObsRuntimeProcess{State: "baseline", Counts: api.ObsRuntimeProcessCounts{Fresh: 41}}
	out.Counts.Matching = 0
	out.Counts.Mismatched = out.Counts.Observed
	m.recordReport(t.Context(), out, time.Now())
	for group, expected := range map[string]int{"identity": out.OmittedItems, "command_cron_outcomes": out.CommandCronOutcomes.OmittedItems, "delayed_task_dispatch": out.DelayedTaskDispatch.OmittedItems, "http_cron_delivery": out.HTTPCronDelivery.OmittedItems} {
		if n := testutil.ToFloat64(m.omitted.WithLabelValues(group)); n != float64(expected) {
			t.Fatalf("%s omissions=%v want %d", group, n, expected)
		}
	}
	if n := testutil.ToFloat64(m.reports.WithLabelValues("complete")); n != 1 {
		t.Fatalf("fresh mismatch treated as incomplete: %v", n)
	}
	assertRuntimeIncompleteGroups(t, m)
	out.CommandCronOutcomes.Incomplete = true
	m.recordReport(t.Context(), out, time.Now())
	if n := testutil.ToFloat64(m.reports.WithLabelValues("incomplete")); n != 1 {
		t.Fatalf("partial advisory evidence ignored: %v", n)
	}
	assertRuntimeIncompleteGroups(t, m, "command_cron_outcomes")
	assertRuntimeCoreEvidenceGaps(t, m, nil)

	out.Jobs[0].State = "unknown"
	out.Counts.Missing = 2
	out.Counts.Stale = 1
	out.Readiness.Counts.Conflicting = 2
	out.Process.Incomplete = true
	out.LoopHealth.Incomplete = true
	multiGroupMetrics := NewRuntimeDiagnosticsMetrics(prometheus.NewPedanticRegistry(), nil)
	multiGroupMetrics.recordReport(t.Context(), out, time.Now())
	if n := testutil.ToFloat64(multiGroupMetrics.reports.WithLabelValues("incomplete")); n != 1 {
		t.Fatalf("incomplete reports = %v, want 1", n)
	}
	assertRuntimeIncompleteGroups(t, multiGroupMetrics, "identity", "readiness", "process", "loop_health", "command_cron_outcomes")
	assertRuntimeGroupEvaluations(t, multiGroupMetrics,
		[]string{"delayed_task_dispatch", "http_cron_delivery"},
		[]string{"identity", "readiness", "process", "loop_health", "command_cron_outcomes"})
}

func TestRuntimeDiagnosticsMetricsCoreGapsPreserveFullSet(t *testing.T) {
	out := runtimeEnvelopeFixture()
	out.Jobs = []api.ObsRuntimeBuildJob{{Job: "apid", State: "available"}, {Job: "vmmd", State: "unknown"}}
	out.Counts.Missing, out.Counts.Stale, out.Counts.Unexpected = 2, 3, 4
	out.Readiness = &api.ObsRuntimeReadiness{Counts: api.ObsRuntimeReadinessCounts{Missing: 2, Conflicting: 3, NotReady: 1}}
	out.Process = &api.ObsRuntimeProcess{Incomplete: true, Counts: api.ObsRuntimeProcessCounts{
		NewTargets: 2, RemovedTargets: 3, RepeatedSamples: 4, ComparisonUnavailable: 5,
	}}
	out.LoopHealth.Incomplete = true
	out.LoopHealth.Counts.Stale, out.LoopHealth.Counts.Unavailable = 2, 3
	// Exercise envelope fitting with the core previews too large to retain.
	out.Items[0].Reason = strings.Repeat("x", api.ObsRuntimeBuildResponseMaxBytes)
	fitted := fitRuntimeLoopResponse(out)
	if len(fitted.Items) != 0 || len(fitted.LoopHealth.Items) != 0 || fitted.Counts != out.Counts {
		t.Fatal("fixture did not trim both core previews while preserving the summary")
	}

	m := NewRuntimeDiagnosticsMetrics(prometheus.NewPedanticRegistry(), nil)
	m.recordReport(t.Context(), fitted, time.Now())
	assertRuntimeCoreEvidenceGaps(t, m, map[string][]string{
		"identity":    {"missing", "stale", "unavailable", "unexpected"},
		"readiness":   {"missing", "conflicting"},
		"process":     {"target_set_changed", "repeated_samples", "comparison_unavailable", "unexpected", "unavailable"},
		"loop_health": {"stale", "unavailable"},
	})
}

func TestRuntimeDiagnosticsMetricsCoreGapsUnavailable(t *testing.T) {
	out := RuntimeBuildsLoopResponse{
		ObsRuntimeBuilds: api.ObsRuntimeBuilds{Jobs: []api.ObsRuntimeBuildJob{{Job: "apid", State: "available"}}},
		LoopHealth:       &RuntimeLoopHealth{RuntimeLoopSummary: RuntimeLoopSummary{Incomplete: true}},
	}
	m := NewRuntimeDiagnosticsMetrics(prometheus.NewPedanticRegistry(), nil)
	m.recordReport(t.Context(), out, time.Now())
	assertRuntimeCoreEvidenceGaps(t, m, map[string][]string{
		"readiness": {"unavailable"}, "process": {"unavailable"}, "loop_health": {"unavailable"},
	})
}

func assertRuntimeCoreEvidenceGaps(t *testing.T, m *RuntimeDiagnosticsMetrics, byGroup map[string][]string) {
	t.Helper()
	for _, group := range runtimeCoreMetricGroups() {
		matched := 0
		for _, gap := range runtimeCoreEvidenceGaps(group, RuntimeBuildsLoopResponse{}) {
			var want float64
			if slices.Contains(byGroup[group], gap.reason) {
				matched++
				want = 1
			}
			if got := testutil.ToFloat64(m.coreGaps.WithLabelValues(group, gap.reason)); got != want {
				t.Errorf("core gap %s/%s = %v, want %v", group, gap.reason, got, want)
			}
		}
		if matched != len(byGroup[group]) {
			t.Errorf("missing expected reason series for %s: want %v", group, byGroup[group])
		}
	}
}

func assertRuntimeGroupEvaluations(t *testing.T, m *RuntimeDiagnosticsMetrics, complete, incomplete []string) {
	t.Helper()
	for _, group := range runtimeMetricGroups() {
		for outcome, groups := range map[string][]string{"complete": complete, "incomplete": incomplete} {
			var want float64
			if slices.Contains(groups, group) {
				want = 1
			}
			if n := testutil.ToFloat64(m.evaluations.WithLabelValues(group, outcome)); n != want {
				t.Errorf("%s evaluations for %s = %v, want %v", outcome, group, n, want)
			}
		}
	}
}

func assertRuntimeIncompleteGroups(t *testing.T, m *RuntimeDiagnosticsMetrics, groups ...string) {
	t.Helper()
	for _, group := range runtimeMetricGroups() {
		var want float64
		if slices.Contains(groups, group) {
			want = 1
		}
		if n := testutil.ToFloat64(m.incomplete.WithLabelValues(group)); n != want {
			t.Errorf("incomplete reports for %s = %v, want %v", group, n, want)
		}
	}
}

func TestRuntimeDiagnosticsMetricsEncodingFailure(t *testing.T) {
	reg := prometheus.NewPedanticRegistry()
	m := NewRuntimeDiagnosticsMetrics(reg, nil)
	out := RuntimeBuildsLoopResponse{ObsRuntimeBuilds: api.ObsRuntimeBuilds{
		Jobs:      []api.ObsRuntimeBuildJob{{Job: "apid", State: "available"}},
		Readiness: &api.ObsRuntimeReadiness{},
		Process:   &api.ObsRuntimeProcess{},
	}}
	out.GeneratedAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	m.recordReport(t.Context(), out, time.Now())
	if testutil.ToFloat64(m.reports.WithLabelValues("encoding_error")) != 1 {
		t.Fatal("encoding failure was not recorded")
	}
	if stamp := testutil.ToFloat64(m.lastComplete); stamp != 0 {
		t.Fatalf("encoding failure updated last complete report timestamp to %v", stamp)
	}
	if stamp := runtimeLastCollectionTimestamp(t, reg, "encoding_error"); stamp == 0 {
		t.Fatal("encoding failure did not update last collection timestamp")
	}
	out.Readiness.Counts.Missing = 1
	m.recordReport(t.Context(), out, time.Now())
	assertRuntimeIncompleteGroups(t, m)
	assertRuntimeGroupEvaluations(t, m, nil, nil)
	assertRuntimeCoreEvidenceGaps(t, m, nil)
	var metric dto.Metric
	if err := m.responseBytes.Write(&metric); err != nil {
		t.Fatal(err)
	}
	if metric.GetHistogram().GetSampleCount() != 0 {
		t.Fatal("invalid payload recorded as encoded bytes")
	}
}

func TestRuntimeDiagnosticsMetricsConcurrentGroupsAndClosedLabels(t *testing.T) {
	reg := prometheus.NewPedanticRegistry()
	m := NewRuntimeDiagnosticsMetrics(reg, nil)
	ctx := context.WithValue(t.Context(), runtimeQueryObservationKey{}, runtimeQueryObservation{metrics: m, group: "identity"})
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			child := runtimeQueryContext(ctx, "process")
			child.Value(runtimeQueryObservationKey{}).(runtimeQueryObservation).record(time.Now(), nil)
		})
	}
	wg.Wait()
	runtimeQueryObservation{metrics: m, group: "private-upstream-secret"}.record(time.Now(), nil)
	if testutil.ToFloat64(m.queries.WithLabelValues("process", "success")) != 32 || testutil.ToFloat64(m.queries.WithLabelValues("identity", "success")) != 0 {
		t.Fatal("concurrent query group attribution changed")
	}
	assertRuntimeMetricsPrivate(t, reg)
}

func runtimeLastCollectionTimestamp(t *testing.T, reg *prometheus.Registry, outcome string) float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != "apid_operator_runtime_last_collection_timestamp_seconds" {
			continue
		}
		metrics := family.GetMetric()
		if len(metrics) != 1 {
			t.Fatalf("last collection has %d samples, want one", len(metrics))
		}
		labels := metrics[0].GetLabel()
		if len(labels) != 1 || labels[0].GetName() != "outcome" || labels[0].GetValue() != outcome {
			t.Fatalf("last collection labels = %v, want outcome=%s", labels, outcome)
		}
		return metrics[0].GetGauge().GetValue()
	}
	t.Fatal("last collection metric missing")
	return 0
}

func assertRuntimeMetricsPrivate(t *testing.T, reg *prometheus.Registry) {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	if len(families) != 17 {
		t.Fatalf("metric families=%d", len(families))
	}
	for _, family := range families {
		for _, metric := range family.Metric {
			for _, label := range metric.Label {
				if label.GetName() != "group" && label.GetName() != "outcome" && label.GetName() != "phase" && label.GetName() != "origin" && label.GetName() != "reason" {
					t.Fatalf("unbounded label %s", label.GetName())
				}
				if label.GetName() == "group" && !runtimeMetricGroupKnown(label.GetValue()) {
					t.Fatalf("unbounded group %s", label.GetValue())
				}
				if label.GetName() == "phase" {
					switch label.GetValue() {
					case "inventory", "core_evidence", "loop_and_advisory", "assembly", "preview_fitting", "before_collection", "outside_phases":
					default:
						t.Fatalf("unbounded phase %s", label.GetValue())
					}
				}
				if label.GetName() == "origin" && label.GetValue() != "collection_budget" && label.GetValue() != "caller_deadline" {
					t.Fatalf("unbounded deadline origin %s", label.GetValue())
				}
				if label.GetName() == "reason" {
					if family.GetName() == "apid_operator_runtime_query_source_errors_total" {
						switch label.GetValue() {
						case "transport_error", "http_error", "invalid_response", "other":
							continue
						default:
							t.Fatalf("unbounded source failure reason %s", label.GetValue())
						}
					}
					switch label.GetValue() {
					case "missing", "stale", "conflicting", "unavailable", "unexpected", "target_set_changed", "repeated_samples", "comparison_unavailable":
					default:
						t.Fatalf("unbounded core evidence reason %s", label.GetValue())
					}
				}
			}
		}
	}
	rec := httptest.NewRecorder()
	promhttp.HandlerFor(reg, promhttp.HandlerOpts{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	for _, private := range []string{"private-host", "private-upstream-secret", strings.Repeat("a", 40), "target-", "http://"} {
		if strings.Contains(rec.Body.String(), private) {
			t.Fatalf("metrics leaked %s", private)
		}
	}
}
