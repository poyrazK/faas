// adr: 1630
package mcpmanagement

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func dependencyImpactFixtureEdge() api.DebugDependencyImpactEdge {
	return api.DebugDependencyImpactEdge{
		From: api.DebugCriticalPathSegment{Type: "application", Name: "request"},
		To:   api.DebugCriticalPathSegment{Type: "binding", Kind: "postgres", Name: "orders " + testToken},
		// Each retained request row contributes two child-span samples, so
		// weighted edge calls legitimately exceed represented requests.
		Calls: 20, ErrorCalls: 14, ErrorRatePct: 70, P50MS: 50, P95MS: 50, P99MS: 50,
		ExclusiveP50MS: 40, ExclusiveP95MS: 40, ExclusiveP99MS: 40, BaselineP95MS: 20, CurrentP95MS: 50, P95DeltaMS: 30,
		BaselineExclusiveP95MS: 10, CurrentExclusiveP95MS: 40, ExclusiveP95DeltaMS: 30,
		RegressionFactor: 2.5, Regression: true, CurrentErrorRatePct: 100, ErrorRateDeltaPct: 100,
		Exemplars: []api.DebugDependencyImpactExemplar{
			{RequestID: "row-1", TraceID: "excluded-separate-trace", Window: "current", ReceivedAt: "2026-10-17T10:59:00Z", DurationMS: 50, HTTPStatus: 503, Error: true, Count: 7},
			{RequestID: "baseline-alias", TraceID: "excluded-baseline-trace", Window: "baseline", ReceivedAt: "2026-10-17T10:20:00Z", DurationMS: 20, HTTPStatus: 200, Count: 3},
		}}
}

func dependencyImpactFixtureWindow() performanceWindow {
	return performanceWindow{App: "alpha", AppID: "app-1", Scope: "app_wide", Since: "1h", WindowStart: "2026-10-17T10:00:01Z", WindowEnd: "2026-10-17T11:00:01Z"}
}

func TestDependencyImpactProjection(t *testing.T) {
	for _, mode := range []string{"observed", "zero_duration", "http_outcome_independent", "trace_alias", "at_cutover", "at_start", "signed_deltas", "no_exemplars", "reused_across_edges"} {
		t.Run(mode, func(t *testing.T) {
			n := performanceFixtureDependencies("app-1")
			n.Complete, n.Truncated = true, false
			e := &n.Edges[0]
			switch mode {
			case "zero_duration":
				e.Exemplars[0].DurationMS = 0
			case "http_outcome_independent":
				e.Exemplars[0].HTTPStatus = 200 // Child span error does not imply HTTP 5xx.
			case "trace_alias":
				e.Exemplars[0].RequestID = "trace-alias:retained/id"
			case "at_cutover":
				e.Exemplars[0].ReceivedAt = "2026-10-17T10:30:01Z"
			case "at_start":
				e.Exemplars[1].ReceivedAt = n.WindowStart
			case "signed_deltas":
				e.P95DeltaMS, e.ExclusiveP95DeltaMS, e.ErrorRateDeltaPct = -10, -5, -10
				e.BaselineP95MS, e.CurrentP95MS, e.Regression, e.RegressionFactor = 60, 50, false, 0.83
				e.BaselineExclusiveP95MS, e.CurrentExclusiveP95MS = 45, 40
				e.BaselineErrorRatePct, e.CurrentErrorRatePct = 80, 70
			case "no_exemplars":
				e.Exemplars = nil
			case "reused_across_edges":
				n.Edges = append(n.Edges, *e)
			}
			m := &management{}
			v, err := m.projectDependencyLatency(dependencyImpactFixtureWindow(), n)
			if err != nil {
				t.Fatal(err)
			}
			row := v.Edges[0]
			if !v.Complete || v.SourceCompleteness != "unknown" || row.SideSampleAvailability != "not_reported" || v.NativeEdgeCount != len(n.Edges) || v.ReturnedEdgeCount != len(n.Edges) || row.Calls <= v.RepresentedRequests || row.From.Name != "request" || !row.LabelsModified || row.P95DeltaMS != e.P95DeltaMS || row.ExclusiveP95DeltaMS != e.ExclusiveP95DeltaMS || row.ErrorRateDeltaPct != e.ErrorRateDeltaPct {
				t.Fatal("edge provenance, weighted counts, ordering or signed trends lost")
			}
			if mode != "no_exemplars" {
				x := row.Exemplars[0]
				if x.LookupIdentifier != e.Exemplars[0].RequestID || x.Count != 7 || x.DurationMS != e.Exemplars[0].DurationMS || x.DurationBasis != "child_span_sample" || !x.Error || x.HTTPStatus != e.Exemplars[0].HTTPStatus || row.Exemplars[1].Window != "baseline" {
					t.Fatal("exemplar selector, child-span basis, zero timing or independent outcomes lost")
				}
			} else if row.Exemplars == nil || row.NativeExemplarCount != 0 || row.ReturnedExemplarCount != 0 {
				t.Fatal("empty exemplar evidence lost")
			}
			encoded, _ := json.Marshal(v)
			for _, marker := range []string{"excluded-", "fp_test_", `"queries"`, `"trace_id"`, `"request_id"`, `"deployment_comparison"`} {
				if strings.Contains(string(encoded), marker) {
					t.Fatalf("unexposed edge data leaked: %s", marker)
				}
			}
			var edge map[string]json.RawMessage
			encoded, _ = json.Marshal(row)
			_ = json.Unmarshal(encoded, &edge)
			if edge["baseline_calls"] != nil || edge["failure_regression"] != nil {
				t.Fatal("invented per-side counts or an unavailable native edge signal")
			}
		})
	}
}

func TestDependencyImpactProjectionBounds(t *testing.T) {
	for _, mode := range []string{"edges", "exemplars", "text", "native_partial", "empty"} {
		t.Run(mode, func(t *testing.T) {
			n := performanceFixtureDependencies("app-1")
			n.Complete, n.Truncated = true, false
			switch mode {
			case "edges":
				for len(n.Edges) <= api.MCPDependencyImpactEdgesMax {
					n.Edges = append(n.Edges, dependencyImpactFixtureEdge())
				}
			case "exemplars":
				for len(n.Edges[0].Exemplars) <= api.MCPDependencyImpactExemplarsMax {
					x := n.Edges[0].Exemplars[0]
					x.RequestID = "sample-" + strconv.Itoa(len(n.Edges[0].Exemplars))
					n.Edges[0].Exemplars = append(n.Edges[0].Exemplars, x)
				}
			case "text":
				n.Edges[0].From.Name = strings.Repeat("x", api.MCPRequestFieldMaxBytes-4) + testToken + strings.Repeat("y", api.MCPRequestFieldMaxBytes)
			case "native_partial":
				n.Complete, n.Truncated = false, true
			case "empty":
				n = emptyPerformanceDependencies("app-1")
			}
			m := &management{}
			v, err := m.projectDependencyLatency(dependencyImpactFixtureWindow(), n)
			if err != nil {
				t.Fatal(err)
			}
			if mode != "empty" && v.Complete {
				t.Fatal("edge/exemplar/source/text limits were hidden by complete")
			}
			switch mode {
			case "edges":
				if !v.EdgesTruncated || v.ReturnedEdgeCount != api.MCPDependencyImpactEdgesMax || v.NativeEdgeCount != api.MCPDependencyImpactEdgesMax+1 {
					t.Fatal("edge prefix limit or counts lost")
				}
			case "exemplars":
				if !v.ExemplarsTruncated || !v.Edges[0].ExemplarsTruncated || v.Edges[0].NativeExemplarCount != api.MCPDependencyImpactExemplarsMax+1 || v.Edges[0].ReturnedExemplarCount != api.MCPDependencyImpactExemplarsMax {
					t.Fatal("exemplar prefix limit or counts lost")
				}
			case "text":
				if !v.TextTruncated || !v.Edges[0].LabelsModified || len(v.Edges[0].From.Name) > api.MCPRequestFieldMaxBytes || strings.Contains(v.Edges[0].From.Name, "fp_test_") {
					t.Fatal("unsafe edge text truncation")
				}
			case "empty":
				if v.Edges == nil || len(v.Edges) != 0 || v.NativeEdgeCount != 0 || v.ReturnedEdgeCount != 0 || v.Status != "no_retained_spans" || v.SourceCompleteness != "unknown" {
					t.Fatal("empty edges were presented as captured topology or health")
				}
			}
		})
	}
}

func TestDependencyImpactRejectsInvalidNativeEvidence(t *testing.T) {
	for _, mode := range []string{"no_spans", "no_represented", "zero_calls", "negative_calls", "too_many_errors", "bad_rate", "bad_baseline_rate", "bad_factor", "negative_percentile", "negative_exclusive_baseline", "negative_exclusive_current", "empty_selector", "dirty_selector", "long_selector", "control_selector", "zero_count", "too_many_edge_calls", "too_many_represented", "too_many_failed_samples", "too_many_successful_samples", "negative_duration", "bad_status", "bad_time", "before_window", "at_end", "wrong_half", "unknown_half", "duplicate_selector"} {
		t.Run(mode, func(t *testing.T) {
			n := performanceFixtureDependencies("app-1")
			e, x := &n.Edges[0], &n.Edges[0].Exemplars[0]
			switch mode {
			case "no_spans":
				n.SpanSamples, n.Dependencies = 0, nil
			case "no_represented":
				n.TelemetryRows, n.RepresentedRequests = 0, 0
			case "zero_calls":
				e.Calls, e.ErrorCalls = 0, 0
			case "negative_calls":
				e.Calls = -1
			case "too_many_errors":
				e.ErrorCalls = e.Calls + 1
			case "bad_rate":
				e.ErrorRatePct = math.NaN()
			case "bad_baseline_rate":
				e.BaselineErrorRatePct = 101
			case "bad_factor":
				e.RegressionFactor = math.Inf(1)
			case "negative_percentile":
				e.P95MS = -1
			case "negative_exclusive_baseline":
				e.BaselineExclusiveP95MS = -1
			case "negative_exclusive_current":
				e.CurrentExclusiveP95MS = -1
			case "empty_selector":
				x.RequestID = "" // A separate trace ID is never used as a fallback.
			case "dirty_selector":
				x.RequestID = testToken
			case "long_selector":
				x.RequestID = strings.Repeat("x", api.MCPRequestIdentifierMaxBytes+1)
			case "control_selector":
				x.RequestID = "row\n1"
			case "zero_count":
				x.Count = 0
			case "too_many_edge_calls":
				x.Count, n.RepresentedRequests = e.Calls+1, e.Calls+1
			case "too_many_represented":
				x.Count = n.RepresentedRequests + 1
			case "too_many_failed_samples":
				e.ErrorCalls = x.Count - 1
			case "too_many_successful_samples":
				x.Error = false
			case "negative_duration":
				x.DurationMS = -1
			case "bad_status":
				x.HTTPStatus = 600
			case "bad_time":
				x.ReceivedAt = "excluded-time"
			case "before_window":
				x.ReceivedAt = "2026-10-17T10:00:00Z"
			case "at_end":
				x.ReceivedAt = n.WindowEnd
			case "wrong_half":
				x.Window = "baseline"
			case "unknown_half":
				x.Window = "excluded-window"
			case "duplicate_selector":
				e.Exemplars = append(e.Exemplars, *x)
			}
			m := &management{}
			if _, err := m.projectDependencyLatency(dependencyImpactFixtureWindow(), n); err == nil {
				t.Fatal("released malformed native dependency edge or exemplar")
			}
		})
	}
}
