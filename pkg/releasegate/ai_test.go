package releasegate

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 20261022153155 — an ai gate fails a release whose AI metric rose
// past its bound over the baseline's, and is not judged without enough
// calls on both sides.

func TestJudgeAI(t *testing.T) {
	gate := func(metric string, increase float64) Gate {
		return Gate{Name: "ai", Kind: KindAI, MetricName: metric, Threshold: increase, MinSamples: 20}
	}
	baseline := AIWindow{Calls: 1000, ErrorCalls: 20, FlaggedCalls: 0, CostMillicents: 100_000, TTFBCalls: 900, TTFBMSTotal: 900 * 400}
	for _, tc := range []struct {
		name       string
		gate       Gate
		release    AIWindow
		baseline   AIWindow
		wantJudged bool
		wantPassed bool
		wantBound  float64
	}{
		// 2% errors before; +50% and the 1-point floor allow up to 3%.
		{"error rate within bound", gate(AIMetricErrorRate, 50), AIWindow{Calls: 100, ErrorCalls: 3}, baseline, true, true, 0.03},
		{"error rate past bound", gate(AIMetricErrorRate, 50), AIWindow{Calls: 100, ErrorCalls: 4}, baseline, true, false, 0.03},
		// No flags before: the floor keeps one flagged call in 200 passing.
		{"flagged rate under the floor", gate(AIMetricFlaggedRate, 10), AIWindow{Calls: 200, FlaggedCalls: 1}, baseline, true, true, 0.01},
		{"flagged rate over the floor", gate(AIMetricFlaggedRate, 10), AIWindow{Calls: 200, FlaggedCalls: 5}, baseline, true, false, 0.01},
		// 100 millicents a call before: +20% allows 120.
		{"cost per call doubled", gate(AIMetricCostPerCall, 20), AIWindow{Calls: 50, CostMillicents: 10_000}, baseline, true, false, 120},
		{"cost per call cheaper", gate(AIMetricCostPerCall, 20), AIWindow{Calls: 50, CostMillicents: 4_000}, baseline, true, true, 120},
		// 400 ms before: +10% is 440 ms, below the 50 ms floor's 450 ms.
		{"ttfb within the floor", gate(AIMetricTTFB, 10), AIWindow{Calls: 30, TTFBCalls: 30, TTFBMSTotal: 30 * 449}, baseline, true, true, 450},
		{"ttfb past the floor", gate(AIMetricTTFB, 10), AIWindow{Calls: 30, TTFBCalls: 30, TTFBMSTotal: 30 * 451}, baseline, true, false, 450},
		{"ttfb counts calls with a first byte", gate(AIMetricTTFB, 10), AIWindow{Calls: 30, TTFBCalls: 19, TTFBMSTotal: 19 * 900}, baseline, false, false, 0},
		{"too few release calls", gate(AIMetricErrorRate, 50), AIWindow{Calls: 19, ErrorCalls: 19}, baseline, false, false, 0},
		{"too few baseline calls", gate(AIMetricErrorRate, 50), AIWindow{Calls: 100, ErrorCalls: 50}, AIWindow{Calls: 19}, false, false, 0},
		{"no baseline", gate(AIMetricErrorRate, 50), AIWindow{Calls: 100, ErrorCalls: 50}, AIWindow{}, false, false, 0},
		{"unknown metric", gate("tokens", 50), AIWindow{Calls: 100}, baseline, false, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := Judge([]Gate{tc.gate}, map[string]Evidence{"ai": {AIRelease: tc.release, AIBaseline: tc.baseline}})
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
			r := v.Results[0]
			if r.Passed != tc.wantPassed || r.Bound != tc.wantBound || r.Kind != KindAI {
				t.Fatalf("result = %+v, want passed %v bound %v", r, tc.wantPassed, tc.wantBound)
			}
		})
	}
}

func TestJudgeAISummaryAndDecision(t *testing.T) {
	g := Gate{Name: "ai-cost", Kind: KindAI, MetricName: AIMetricCostPerCall, Threshold: 50, MinSamples: 10}
	v := Judge([]Gate{g}, map[string]Evidence{"ai-cost": {
		AIRelease:  AIWindow{Calls: 40, CostMillicents: 8_000},
		AIBaseline: AIWindow{Calls: 100, CostMillicents: 10_000},
	}})
	if v.Failed == nil || v.Failed.Observed != 200 || v.Failed.Bound != 150 || v.Failed.Samples != 40 {
		t.Fatalf("verdict = %+v", v)
	}
	want := "release gate ai-cost: cost_per_call $0.00200 over 40 calls vs $0.00100 before (fails above $0.00150, +50%)"
	if got := v.Summary(); got != want {
		t.Fatalf("summary = %q\nwant      %q", got, want)
	}
	decision := api.RolloutDecision{Kind: api.RolloutDecisionAbort, Rule: api.RolloutRuleAbortReleaseGate, Summary: v.Summary(),
		Gates: v.Results, DecidedAt: "2026-10-22T10:00:00Z"}
	if err := decision.Validate(); err != nil {
		t.Fatalf("decision with an ai result does not validate: %v", err)
	}
}

func TestFromRequestAI(t *testing.T) {
	threshold := 1.0
	for _, tc := range []struct {
		name     string
		req      api.CreateReleaseGateRequest
		wantErr  string
		increase float64
	}{
		{name: "default increase", req: api.CreateReleaseGateRequest{Name: "ai", Kind: KindAI, Metric: AIMetricErrorRate}, increase: 50},
		{name: "explicit increase", req: api.CreateReleaseGateRequest{Name: "ai", Kind: KindAI, Metric: AIMetricTTFB, MaxIncreasePercent: 1000}, increase: 1000},
		{name: "unknown metric", req: api.CreateReleaseGateRequest{Name: "ai", Kind: KindAI, Metric: "tokens"}, wantErr: "metric must be"},
		{name: "increase too large", req: api.CreateReleaseGateRequest{Name: "ai", Kind: KindAI, Metric: AIMetricErrorRate, MaxIncreasePercent: 1001}, wantErr: "1-1000"},
		{name: "negative increase", req: api.CreateReleaseGateRequest{Name: "ai", Kind: KindAI, Metric: AIMetricErrorRate, MaxIncreasePercent: -5}, wantErr: "1-1000"},
		{name: "custom metric fields", req: api.CreateReleaseGateRequest{Name: "ai", Kind: KindAI, Metric: AIMetricErrorRate, Threshold: &threshold}, wantErr: "takes only"},
		{name: "slo fields", req: api.CreateReleaseGateRequest{Name: "ai", Kind: KindAI, Metric: AIMetricErrorRate, SLO: "checkout"}, wantErr: "takes only"},
		{name: "increase on another kind", req: api.CreateReleaseGateRequest{Name: "x", Kind: KindNewErrors, MaxIncreasePercent: 10}, wantErr: "takes only"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, p := FromRequest(tc.req)
			if tc.wantErr != "" {
				if p == nil || !strings.Contains(p.Detail, tc.wantErr) {
					t.Fatalf("problem = %v, want %q", p, tc.wantErr)
				}
				return
			}
			if p != nil || s.Kind != KindAI || s.MetricName != tc.req.Metric || s.Threshold != tc.increase || s.Comparison != "" {
				t.Fatalf("spec = %+v, problem %v", s, p)
			}
		})
	}
}
