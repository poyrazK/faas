package releasegate

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 20261019071734 — how each release gate kind judges a release.

func TestJudge(t *testing.T) {
	availability := Gate{Name: "checkout", Kind: KindSLO, ObjectiveBP: 9990, MaxBurnRate: 10, MinSamples: 20}
	latency := Gate{Name: "fast", Kind: KindSLO, ObjectiveBP: 9900, LatencyThresholdMS: 250, MaxBurnRate: 5, MinSamples: 20}
	newErrors := Gate{Name: "errors", Kind: KindNewErrors, MaxNewGroups: 0, MinEvents: 3}
	metric := Gate{Name: "orders", Kind: KindCustomMetric, MetricName: "orders", Comparison: "below", Threshold: 5, MinSamples: 3}
	for _, tc := range []struct {
		name       string
		gate       Gate
		ev         Evidence
		wantJudged bool
		wantPassed bool
	}{
		// 99.9% leaves a 0.1% budget: 2% bad is a 20x burn.
		{"slo burning fast and worse than baseline", availability, Evidence{Release: SLISample{Requests: 500, ServerErrors: 10}, Baseline: SLISample{Requests: 5000}}, true, false},
		{"slo burning but no worse than baseline", availability, Evidence{Release: SLISample{Requests: 500, ServerErrors: 10}, Baseline: SLISample{Requests: 5000, ServerErrors: 150}}, true, true},
		{"slo within budget", availability, Evidence{Release: SLISample{Requests: 5000, ServerErrors: 2}}, true, true},
		{"slo without a baseline fails on burn alone", availability, Evidence{Release: SLISample{Requests: 500, ServerErrors: 10}}, true, false},
		{"slo with too few requests", availability, Evidence{Release: SLISample{Requests: 19, ServerErrors: 19}}, false, false},
		{"latency slo counts slow requests", latency, Evidence{Release: SLISample{Requests: 100, ServerErrors: 0, SlowRequests: 20}, Baseline: SLISample{Requests: 100, SlowRequests: 1}}, true, false},
		{"latency slo ignores 5xx", latency, Evidence{Release: SLISample{Requests: 100, ServerErrors: 50, SlowRequests: 0}}, true, true},
		{"one new error group fails max 0", newErrors, Evidence{NewGroups: []ErrorGroup{{ErrorClass: "null_pointer", Events: 12}}}, true, false},
		{"small new groups do not count", newErrors, Evidence{NewGroups: []ErrorGroup{{ErrorClass: "null_pointer", Events: 2}}}, true, true},
		{"metric below its bound", metric, Evidence{Metric: MetricWindow{Samples: 10, Average: 1.5}}, true, false},
		{"metric above its bound", metric, Evidence{Metric: MetricWindow{Samples: 10, Average: 9}}, true, true},
		{"metric with too few values", metric, Evidence{Metric: MetricWindow{Samples: 2, Average: 0}}, false, false},
		{"unavailable evidence", availability, Evidence{Unavailable: true}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := Judge([]Gate{tc.gate}, map[string]Evidence{tc.gate.Name: tc.ev})
			if len(v.Results) != 1 {
				t.Fatalf("results = %+v, want the gate reported", v.Results)
			}
			if judged := !v.Results[0].NotJudged; judged != tc.wantJudged {
				t.Fatalf("judged = %v, want %v (%+v)", judged, tc.wantJudged, v)
			}
			if !tc.wantJudged {
				// An unjudged gate says why and never fails the verdict.
				if !v.Results[0].Passed || v.Failed != nil || !strings.HasPrefix(v.Results[0].Summary, "not judged: ") {
					t.Fatalf("unjudged result = %+v, failed = %+v", v.Results[0], v.Failed)
				}
				return
			}
			if v.Results[0].Passed != tc.wantPassed {
				t.Fatalf("passed = %v, want %v: %s", v.Results[0].Passed, tc.wantPassed, v.Results[0].Summary)
			}
			if (v.Failed != nil) != !tc.wantPassed {
				t.Fatalf("failed = %+v", v.Failed)
			}
		})
	}
}

func TestJudgeReportsTheFirstFailingGateByName(t *testing.T) {
	gates := []Gate{
		{Name: "zeta", Kind: KindNewErrors, MinEvents: 1},
		{Name: "alpha", Kind: KindNewErrors, MinEvents: 1},
	}
	ev := Evidence{NewGroups: []ErrorGroup{{ErrorClass: "db_timeout", Events: 5}}}
	v := Judge(gates, map[string]Evidence{"zeta": ev, "alpha": ev})
	if v.Failed == nil || v.Failed.Name != "alpha" || len(v.Results) != 2 {
		t.Fatalf("verdict = %+v", v)
	}
	if got := v.Summary(); got != "release gate alpha: 1 new error groups (max 0): db_timeout" {
		t.Fatalf("summary = %q", got)
	}
	decision := api.RolloutDecision{Kind: api.RolloutDecisionAbort, Rule: api.RolloutRuleAbortReleaseGate, Summary: v.Summary(),
		Gates: v.Results, DecidedAt: "2026-10-19T10:00:00Z"}
	if err := decision.Validate(); err != nil {
		t.Fatalf("decision with gate results does not validate: %v", err)
	}
}

func TestFromRequest(t *testing.T) {
	zero := 0
	threshold := 2.5
	for _, tc := range []struct {
		name    string
		req     api.CreateReleaseGateRequest
		wantErr bool
		check   func(t *testing.T, s Spec)
	}{
		{name: "slo defaults", req: api.CreateReleaseGateRequest{Name: "checkout", Kind: KindSLO, SLO: "checkout"},
			check: func(t *testing.T, s Spec) {
				if s.MaxBurnRate != 10 || s.MinSamples != 20 || s.SLOName != "checkout" {
					t.Fatalf("spec = %+v", s)
				}
			}},
		{name: "new errors defaults", req: api.CreateReleaseGateRequest{Name: "errors", Kind: KindNewErrors},
			check: func(t *testing.T, s Spec) {
				if s.MaxNewGroups != 0 || s.MinEvents != 3 {
					t.Fatalf("spec = %+v", s)
				}
			}},
		{name: "new errors explicit zero", req: api.CreateReleaseGateRequest{Name: "errors", Kind: KindNewErrors, MaxNewGroups: &zero, MinEvents: 1}},
		{name: "custom metric", req: api.CreateReleaseGateRequest{Name: "orders", Kind: KindCustomMetric, Metric: "orders", Comparison: "below", Threshold: &threshold},
			check: func(t *testing.T, s Spec) {
				if s.Threshold != 2.5 || s.Comparison != "below" {
					t.Fatalf("spec = %+v", s)
				}
			}},
		{name: "slo without slo name", req: api.CreateReleaseGateRequest{Name: "x", Kind: KindSLO}, wantErr: true},
		{name: "mixed kinds", req: api.CreateReleaseGateRequest{Name: "x", Kind: KindSLO, SLO: "a", Metric: "b"}, wantErr: true},
		{name: "metric without threshold", req: api.CreateReleaseGateRequest{Name: "x", Kind: KindCustomMetric, Metric: "orders", Comparison: "below"}, wantErr: true},
		{name: "bad comparison", req: api.CreateReleaseGateRequest{Name: "x", Kind: KindCustomMetric, Metric: "orders", Comparison: "equal", Threshold: &threshold}, wantErr: true},
		{name: "bad name", req: api.CreateReleaseGateRequest{Name: "Checkout", Kind: KindNewErrors}, wantErr: true},
		{name: "unknown kind", req: api.CreateReleaseGateRequest{Name: "x", Kind: "latency"}, wantErr: true},
		{name: "burn rate too high", req: api.CreateReleaseGateRequest{Name: "x", Kind: KindSLO, SLO: "a", MaxBurnRate: 1001}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, p := FromRequest(tc.req)
			if tc.wantErr != (p != nil) {
				t.Fatalf("problem = %v, wantErr %v", p, tc.wantErr)
			}
			if tc.check != nil {
				tc.check(t, s)
			}
		})
	}
}
