package operatorobs

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
)

const servingFixtureURL = "https://probe.example/private?token=private-token"

type servingFixture struct {
	rows    [4][]promql.VectorSample
	fail    [4]bool
	warning [4]bool
	block   bool
}

func servingFixtureRow(metric, node, targetURL string, value float64, recording bool) promql.VectorSample {
	labels := map[string]string{probeMetricLabel: metric}
	if !recording {
		labels["job"], labels["instance"] = "node", "private-cp:9100"
	}
	if node != "" {
		labels["probe_node"] = node
	}
	if targetURL != "" {
		labels["url"] = targetURL
	}
	return promql.VectorSample{Labels: labels, Value: value}
}

func healthyServingFixture(now time.Time) servingFixture {
	f := servingFixture{}
	f.rows[0] = []promql.VectorSample{servingFixtureRow(probeConfigured, "", "", 1, false), servingFixtureRow(probePairs, "", "", 2, false), servingFixtureRow("up", "", "", 1, false)}
	for _, node := range []string{"fsn-2", "fsn-3"} {
		for metric, value := range map[string]float64{probeExpected: 1, probeBudget: 2, probeAge: 900} {
			target := ""
			if metric == probeExpected {
				target = servingFixtureURL
			}
			f.rows[0] = append(f.rows[0], servingFixtureRow(metric, node, target, value, false))
		}
		for metric, value := range map[string]float64{probeState: 0, probeEvalBudget: 2, probeEvalAge: 900, probeRun: float64(now.Add(-time.Minute).Unix())} {
			target := ""
			if metric == probeState {
				target = servingFixtureURL
			}
			f.rows[2] = append(f.rows[2], servingFixtureRow(metric, node, target, value, true))
		}
	}
	for _, index := range []int{0, 2} {
		for _, row := range f.rows[index] {
			f.rows[index+1] = append(f.rows[index+1], servingFixtureRow(row.Labels[probeMetricLabel], row.Labels["probe_node"], row.Labels["url"], float64(now.Add(-10*time.Second).Unix()), index == 2))
		}
	}
	return f
}

func (f *servingFixture) change(index int, metric, node string, update func(*promql.VectorSample)) {
	for i := range f.rows[index] {
		if f.rows[index][i].Labels[probeMetricLabel] == metric && f.rows[index][i].Labels["probe_node"] == node {
			update(&f.rows[index][i])
		}
	}
}

func (f *servingFixture) remove(index int, metric, node string) {
	rows := []promql.VectorSample{}
	for _, row := range f.rows[index] {
		if row.Labels[probeMetricLabel] != metric || node != "*" && row.Labels["probe_node"] != node {
			rows = append(rows, row)
		}
	}
	f.rows[index] = rows
}

func (f servingFixture) server(t *testing.T, now time.Time) *httptest.Server {
	t.Helper()
	queries := servingHealthQueries()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/api/v1/query" || r.URL.Query().Get("time") != now.UTC().Format(time.RFC3339Nano) {
			t.Error("serving evidence was not pinned to one evaluation time")
		}
		for i, q := range queries {
			if r.URL.Query().Get("query") != q {
				continue
			}
			if f.block {
				<-r.Context().Done()
				return
			}
			if f.fail[i] {
				http.Error(w, "private provider credentials", 503)
				return
			}
			rows := []any{}
			for _, row := range f.rows[i] {
				rows = append(rows, map[string]any{"metric": row.Labels, "value": []any{now.Unix(), strconv.FormatFloat(row.Value, 'f', -1, 64)}})
			}
			payload := map[string]any{"status": "success", "data": map[string]any{"resultType": "vector", "result": rows}}
			if f.warning[i] {
				payload["warnings"] = []string{"private upstream warning"}
			}
			_ = json.NewEncoder(w).Encode(payload)
			return
		}
		t.Error("unexpected serving query")
		http.Error(w, "unknown", 400)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestServingHealthCoverageEvidence(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	set := func(value float64) func(*promql.VectorSample) {
		return func(row *promql.VectorSample) { row.Value = value }
	}
	cases := []struct {
		name, state, probeState, reason string
		mutate                          func(*servingFixture)
	}{
		{"healthy fleet", "healthy", "healthy", "", func(*servingFixture) {}},
		{"missing node", "degraded", "missing", "result_missing", func(f *servingFixture) {
			f.change(2, probeState, "fsn-2", set(1))
			f.remove(2, probeRun, "fsn-2")
			f.remove(3, probeRun, "fsn-2")
		}},
		{"stale result", "degraded", "stale", "result_stale", func(f *servingFixture) { f.change(2, probeState, "fsn-2", set(2)) }},
		{"invalid telemetry", "degraded", "invalid_telemetry", "invalid_telemetry", func(f *servingFixture) { f.change(2, probeState, "fsn-2", set(3)) }},
		{"disabled validation", "degraded", "content_disabled", "content_check_disabled", func(f *servingFixture) { f.change(2, probeState, "fsn-2", set(4)) }},
		{"budget drift", "degraded", "budget_mismatch", "latency_budget_mismatch", func(f *servingFixture) { f.change(2, probeState, "fsn-2", set(5)) }},
		{"failing probe", "degraded", "failing", "probe_failing", func(f *servingFixture) { f.change(2, probeState, "fsn-2", set(6)) }},
		{"absent evaluation", "partial", "unknown", "coverage_missing", func(f *servingFixture) { f.remove(2, probeState, "fsn-2"); f.remove(3, probeState, "fsn-2") }},
		{"stopped evaluation", "partial", "unknown", "coverage_stale", func(f *servingFixture) {
			f.change(3, probeState, "fsn-2", set(float64(now.Add(-3*time.Minute).Unix())))
		}},
		{"future evaluation", "partial", "unknown", "coverage_invalid", func(f *servingFixture) { f.change(3, probeState, "fsn-2", set(float64(now.Add(time.Minute).Unix()))) }},
		{"nonintegral state", "partial", "unknown", "coverage_invalid", func(f *servingFixture) { f.change(2, probeState, "fsn-2", set(0.5)) }},
		{"new policy awaits evaluation", "partial", "unknown", "policy_not_evaluated", func(f *servingFixture) { f.change(0, probeBudget, "fsn-2", set(3)) }},
		{"mixed evaluations", "partial", "unknown", "policy_not_evaluated", func(f *servingFixture) {
			f.change(3, probeEvalBudget, "fsn-2", set(float64(now.Add(-20*time.Second).Unix())))
		}},
		{"frozen run cannot hold green", "degraded", "stale", "result_stale", func(f *servingFixture) {
			f.change(2, probeRun, "fsn-2", set(float64(now.Add(-901*time.Second).Unix())))
		}},
		{"future run clock", "partial", "unknown", "run_clock_unavailable", func(f *servingFixture) { f.change(2, probeRun, "fsn-2", set(float64(now.Add(time.Minute).Unix()))) }},
		{"missing run clock", "partial", "unknown", "run_clock_unavailable", func(f *servingFixture) { f.remove(2, probeRun, "fsn-2") }},
		{"record query failed", "partial", "unknown", "query_failed", func(f *servingFixture) { f.fail[2] = true }},
		{"failure survives unknown sibling", "degraded", "failing", "probe_failing", func(f *servingFixture) {
			f.change(2, probeState, "fsn-2", set(6))
			f.remove(2, probeState, "fsn-3")
			f.remove(3, probeState, "fsn-3")
		}},
		{"another URL cannot satisfy intent", "partial", "unknown", "coverage_missing", func(f *servingFixture) {
			for _, i := range []int{2, 3} {
				f.change(i, probeState, "fsn-2", func(row *promql.VectorSample) { row.Labels["url"] = "https://other.example/private-token" })
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := healthyServingFixture(now)
			tc.mutate(&f)
			srv := f.server(t, now)
			out := ServingHealth(t.Context(), promql.NewClient(srv.URL, srv.Client()), now)
			if out.State != tc.state || len(out.Probes) != 2 || out.Probes[0].Node != "fsn-2" || out.Probes[0].State != tc.probeState || out.Probes[0].Reason != tc.reason || out.Counts == nil || out.Counts.Expected != 2 {
				t.Fatalf("serving coverage: %+v", out)
			}
			if out.Probes[0].URLID != servingProbeURLID(servingFixtureURL) {
				t.Fatal("exact URL identity changed")
			}
			body, _ := json.Marshal(out)
			if strings.Contains(string(body), "private") || strings.Contains(string(body), "https://") || strings.Contains(string(body), srv.URL) {
				t.Fatalf("private source metadata leaked: %s", body)
			}
		})
	}
}

func TestServingHealthInventoryUnknownVersusExplicitEmpty(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, mode := range []string{"unconfigured", "empty", "missing", "stale", "future", "count mismatch", "wrong source", "down inventory node", "duplicate", "bad URL", "unknown node", "query error", "warning", "nonfinite"} {
		t.Run(mode, func(t *testing.T) {
			f := healthyServingFixture(now)
			switch mode {
			case "unconfigured", "empty":
				for _, i := range []int{0, 1} {
					for _, m := range []string{probeExpected, probeBudget, probeAge} {
						f.remove(i, m, "*")
					}
				}
				f.change(0, probePairs, "", func(row *promql.VectorSample) { row.Value = 0 })
				if mode == "unconfigured" {
					f.change(0, probeConfigured, "", func(row *promql.VectorSample) { row.Value = 0 })
				}
			case "missing":
				f.rows[0], f.rows[1] = nil, nil
			case "stale":
				f.change(1, probePairs, "", func(row *promql.VectorSample) { row.Value = float64(now.Add(-3 * time.Minute).Unix()) })
			case "future":
				f.change(1, probeConfigured, "", func(row *promql.VectorSample) { row.Value = float64(now.Add(time.Minute).Unix()) })
			case "count mismatch":
				f.change(0, probePairs, "", func(row *promql.VectorSample) { row.Value = 3 })
			case "wrong source":
				for _, i := range []int{0, 1} {
					f.change(i, probePairs, "", func(row *promql.VectorSample) { row.Labels["instance"] = "private-other:9100" })
				}
			case "down inventory node":
				f.change(0, "up", "", func(row *promql.VectorSample) { row.Value = 0 })
			case "duplicate":
				f.rows[0] = append(f.rows[0], f.rows[0][0])
			case "bad URL":
				f.change(0, probeExpected, "fsn-2", func(row *promql.VectorSample) { row.Labels["url"] = "https://private-user:private-secret@example/" })
			case "unknown node":
				f.change(0, probeExpected, "fsn-2", func(row *promql.VectorSample) { row.Labels["probe_node"] = "private\nnode" })
			case "query error":
				f.fail[0] = true
			case "warning":
				f.warning[1] = true
			case "nonfinite":
				f.rows[0][0].Value = math.NaN()
			}
			srv := f.server(t, now)
			out := ServingHealth(t.Context(), promql.NewClient(srv.URL, srv.Client()), now)
			if mode == "empty" {
				if out.State != "not_applicable" || out.Counts == nil || out.Counts.Expected != 0 || out.Inventory.Configured == nil || !*out.Inventory.Configured {
					t.Fatalf("explicit empty: %+v", out)
				}
			} else if out.State != "partial" || out.Counts != nil || len(out.Probes) != 0 {
				t.Fatalf("unknown inventory: %+v", out)
			}
			if mode == "unconfigured" && (out.Inventory.Configured == nil || *out.Inventory.Configured || out.Reason != "inventory_not_configured") {
				t.Fatal("unconfigured intent was confused with empty intent")
			}
			body, _ := json.Marshal(out)
			if strings.Contains(string(body), "private") || strings.Contains(string(body), srv.URL) {
				t.Fatalf("source metadata leaked: %s", body)
			}
		})
	}
	if out := ServingHealth(t.Context(), nil, now); out.State != "partial" || out.Reason != "prometheus_not_configured" || out.Counts != nil {
		t.Fatal("missing source fabricated coverage")
	}
}

func TestServingHealthSharedCancellation(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	f := healthyServingFixture(now)
	f.block = true
	srv := f.server(t, now)
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
	defer cancel()
	start := time.Now()
	if out := ServingHealth(ctx, promql.NewClient(srv.URL, srv.Client()), now); time.Since(start) > time.Second || out.State != "partial" || out.Counts != nil {
		t.Fatal("serving reads did not share cancellation")
	}
}

func TestServingHealthBoundsAndExactURLIdentity(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, size := range []int{api.ObsServingProbePairsMax, api.ObsServingProbePairsMax + 1} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			f := healthyServingFixture(now)
			for _, i := range []int{0, 1} {
				f.remove(i, probeExpected, "*")
				f.remove(i, probeBudget, "fsn-3")
				f.remove(i, probeAge, "fsn-3")
			}
			f.change(0, probePairs, "", func(row *promql.VectorSample) { row.Value = float64(size) })
			for i := 0; i < size; i++ {
				url := servingFixtureURL + "&pair=" + strconv.Itoa(i)
				for _, index := range []int{0, 1, 2, 3} {
					metric, value, recording := probeExpected, 1.0, false
					if index >= 2 {
						metric, value, recording = probeState, 0, true
					}
					if index%2 == 1 {
						value = float64(now.Add(-10 * time.Second).Unix())
					}
					f.rows[index] = append(f.rows[index], servingFixtureRow(metric, "fsn-2", url, value, recording))
				}
			}
			// Remove the initial two state rows; only the constructed fleet is current.
			for _, i := range []int{2, 3} {
				rows := []promql.VectorSample{}
				for _, row := range f.rows[i] {
					if row.Labels[probeMetricLabel] == probeState && row.Labels["url"] == servingFixtureURL {
						continue
					}
					rows = append(rows, row)
				}
				f.rows[i] = rows
			}
			srv := f.server(t, now)
			out := ServingHealth(t.Context(), promql.NewClient(srv.URL, srv.Client()), now)
			if size <= api.ObsServingProbePairsMax {
				if out.State != "healthy" || len(out.Probes) != size || out.Counts == nil || out.Counts.Healthy != size {
					t.Fatalf("bounded fleet lost evidence: state=%s pairs=%d", out.State, len(out.Probes))
				}
			} else if out.State != "partial" || out.Counts != nil || len(out.Probes) != 0 {
				t.Fatal("over-bound fleet was truncated into a usable assessment")
			}
		})
	}
	if servingProbeURLID(servingFixtureURL) == servingProbeURLID(servingFixtureURL+"&other=1") {
		t.Fatal("distinct exact URLs lost their identity")
	}
}

func TestServingHealthQueriesHaveSemanticFixtures(t *testing.T) {
	body, err := os.ReadFile("../../promqlrules/testdata/operator_serving_health.test.yml")
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range servingHealthQueries() {
		if !strings.Contains(string(body), query) {
			t.Fatal("serving query lacks a real Prometheus fixture")
		}
	}
}
