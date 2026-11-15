package operatorobs

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
)

func (f *servingFixture) diagnostics(node, target string, values [4]float64, now time.Time) {
	for i, metric := range []string{probeFailure, probeFirstFailure, probeAttempts, probeHTTPStatus} {
		f.rows[4] = append(f.rows[4], servingFixtureRow(metric, node, target, values[i], true))
		f.rows[5] = append(f.rows[5], servingFixtureRow(metric, node, target, float64(now.Add(-10*time.Second).Unix()), true))
	}
}

func TestServingProbeDiagnostics(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, tc := range []struct {
		name, reason, code string
		values             [4]float64
		failing, recovered bool
		mutate             func(*servingFixture)
	}{
		{name: "first attempt passed", code: "none", values: [4]float64{0, 0, 1, 200}},
		{name: "retry recovered", code: "none", values: [4]float64{0, 5, 2, 200}, recovered: true},
		{name: "TLS failure", code: "tls", values: [4]float64{3, 3, 2, 0}, failing: true},
		{name: "DNS failure", code: "dns", values: [4]float64{1, 1, 2, 0}, failing: true},
		{name: "connection failure", code: "connection", values: [4]float64{2, 2, 2, 0}, failing: true},
		{name: "body failure despite 200", code: "body_mismatch", values: [4]float64{7, 7, 2, 200}, failing: true},
		{name: "latency failure", code: "latency", values: [4]float64{6, 4, 2, 200}, failing: true},
		{name: "timeout after headers", code: "timeout", values: [4]float64{4, 4, 2, 200}, failing: true},
		{name: "unclassified failure", code: "unknown", values: [4]float64{8, 4, 2, 0}, failing: true},
		{name: "HTTP redirect", code: "http_status", values: [4]float64{5, 5, 2, 301}, failing: true},
		{name: "future code", values: [4]float64{99, 99, 2, 0}, reason: "diagnostics_inconsistent"},
		{name: "fractional attempt", values: [4]float64{0, 5, 1.5, 200}, reason: "diagnostics_inconsistent"},
		{name: "unnecessary retry", values: [4]float64{0, 0, 2, 200}, reason: "diagnostics_inconsistent"},
		{name: "failure without required retry", values: [4]float64{5, 5, 1, 500}, failing: true, reason: "diagnostics_inconsistent"},
		{name: "success with error status", values: [4]float64{0, 5, 2, 500}, reason: "diagnostics_inconsistent"},
		{name: "failure contradicts coverage", values: [4]float64{3, 3, 2, 0}, reason: "diagnostics_inconsistent"},
		{name: "missing attempt metadata", values: [4]float64{0, 5, 2, 200}, reason: "diagnostics_missing", mutate: func(f *servingFixture) { f.remove(4, probeAttempts, "fsn-2") }},
		{name: "mixed evaluations", values: [4]float64{0, 5, 2, 200}, reason: "diagnostics_not_current", mutate: func(f *servingFixture) {
			f.change(5, probeAttempts, "fsn-2", func(row *promql.VectorSample) { row.Value -= 60 })
		}},
		{name: "query failure", values: [4]float64{0, 5, 2, 200}, reason: "query_failed", mutate: func(f *servingFixture) { f.fail[4] = true }},
		{name: "partial query warning", values: [4]float64{0, 5, 2, 200}, reason: "query_failed", mutate: func(f *servingFixture) { f.warning[5] = true }},
		{name: "duplicate metadata", values: [4]float64{0, 5, 2, 200}, reason: "query_failed", mutate: func(f *servingFixture) { f.rows[4] = append(f.rows[4], f.rows[4][0]) }},
		{name: "private extra label", values: [4]float64{0, 5, 2, 200}, reason: "query_failed", mutate: func(f *servingFixture) { f.rows[4][0].Labels["private"] = "private-provider-error" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := healthyServingFixture(now)
			f.diagnostics("fsn-2", servingFixtureURL, tc.values, now)
			if tc.failing {
				f.change(2, probeState, "fsn-2", func(row *promql.VectorSample) { row.Value = 6 })
			}
			if tc.mutate != nil {
				tc.mutate(&f)
			}
			srv := f.server(t, now)
			out := ServingHealth(t.Context(), promql.NewClient(srv.URL, srv.Client()), now)
			wantCoverage := "healthy"
			if tc.failing {
				wantCoverage = "degraded"
			}
			if out.State != wantCoverage || len(out.Probes) != 2 {
				t.Fatalf("diagnostics changed coverage: %+v", out)
			}
			d := out.Probes[0].Diagnostics
			if !ServingProbeDiagnosticsValid(out.Probes[0], now) {
				t.Fatalf("API emitted invalid diagnostics: %+v", d)
			}
			if tc.reason == "" {
				if d.State != "available" || d.FailureCode != tc.code || *d.Attempts != int(tc.values[2]) || *d.RecoveredOnRetry != tc.recovered {
					t.Fatalf("diagnostics lost failure/retry information: %+v", d)
				}
			} else if d.State != "unknown" || d.Reason != tc.reason || d.Attempts != nil || d.FailureCode != "" {
				t.Fatalf("unavailable diagnostics became known: %+v", d)
			}
			if sibling := out.Probes[1].Diagnostics; sibling.State != "unknown" {
				t.Fatal("an older probe inherited another pair's diagnostics")
			}
			body, _ := json.Marshal(out)
			if strings.Contains(string(body), "private") || strings.Contains(string(body), "https://") {
				t.Fatalf("diagnostics leaked raw source metadata: %s", body)
			}
		})
	}
}

func TestServingProbeDiagnosticsMaximumFleetFitsStrictQueryBounds(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	f := healthyServingFixture(now)
	for i := range f.rows {
		f.remove(i, probeExpected, "*")
		f.remove(i, probeState, "*")
		for _, metric := range []string{probeBudget, probeAge, probeEvalBudget, probeEvalAge, probeRun} {
			f.remove(i, metric, "fsn-3")
		}
	}
	f.change(0, probePairs, "", func(row *promql.VectorSample) { row.Value = api.ObsServingProbePairsMax })
	for i := 0; i < api.ObsServingProbePairsMax; i++ {
		target := "https://probe.example/" + strconv.Itoa(i)
		f.rows[0] = append(f.rows[0], servingFixtureRow(probeExpected, "fsn-2", target, 1, false))
		f.rows[1] = append(f.rows[1], servingFixtureRow(probeExpected, "fsn-2", target, float64(now.Add(-10*time.Second).Unix()), false))
		f.rows[2] = append(f.rows[2], servingFixtureRow(probeState, "fsn-2", target, 0, true))
		f.rows[3] = append(f.rows[3], servingFixtureRow(probeState, "fsn-2", target, float64(now.Add(-10*time.Second).Unix()), true))
		f.diagnostics("fsn-2", target, [4]float64{0, 5, 2, 200}, now)
	}
	srv := f.server(t, now)
	out := ServingHealth(t.Context(), promql.NewClient(srv.URL, srv.Client()), now)
	if out.State != "healthy" || len(out.Probes) != api.ObsServingProbePairsMax {
		t.Fatalf("maximum fleet failed: state=%s probes=%d", out.State, len(out.Probes))
	}
	for _, probe := range out.Probes {
		if probe.Diagnostics == nil || probe.Diagnostics.State != "available" || !*probe.Diagnostics.RecoveredOnRetry {
			t.Fatal("4000-series diagnostics vector was truncated or discarded")
		}
	}
}
