package releasegate

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 20261022082305 — synthetic gates compare the candidate's probes with
// the stable revision's; metric_change gates compare with the metric before
// the rollout.
func TestJudgeSyntheticAndMetricChange(t *testing.T) {
	synthetic := Gate{Name: "checkout-probe", Kind: KindSynthetic, CheckName: "checkout", MaxFailurePercent: 10, MinSamples: 6}
	drop := Gate{Name: "orders-drop", Kind: KindMetricChange, MetricName: "orders", Comparison: "below", MaxChangePercent: 30, MinSamples: 3}
	rise := Gate{Name: "latency-rise", Kind: KindMetricChange, MetricName: "latency_ms", Comparison: "above", MaxChangePercent: 50, MinSamples: 3}
	for _, tc := range []struct {
		name       string
		gate       Gate
		ev         Evidence
		wantJudged bool
		wantPassed bool
	}{
		{"candidate fails its check", synthetic, Evidence{Probes: ProbeCounts{Requests: 8, Failures: 5}, BaselineProbes: ProbeCounts{Requests: 8}}, true, false},
		{"both revisions fail the check", synthetic, Evidence{Probes: ProbeCounts{Requests: 8, Failures: 5}, BaselineProbes: ProbeCounts{Requests: 8, Failures: 6}}, true, true},
		{"within the allowed failures", synthetic, Evidence{Probes: ProbeCounts{Requests: 20, Failures: 2}, BaselineProbes: ProbeCounts{Requests: 20}}, true, true},
		{"too few probes", synthetic, Evidence{Probes: ProbeCounts{Requests: 5, Failures: 5}}, false, false},
		{"no stable probes counts as zero", synthetic, Evidence{Probes: ProbeCounts{Requests: 6, Failures: 6}}, true, false},
		{"metric dropped too far", drop, Evidence{Metric: MetricWindow{Samples: 10, Average: 31}, PriorMetric: MetricWindow{Samples: 10, Average: 53}}, true, false},
		{"metric dropped a little", drop, Evidence{Metric: MetricWindow{Samples: 10, Average: 48}, PriorMetric: MetricWindow{Samples: 10, Average: 53}}, true, true},
		{"metric rose, gate watches drops", drop, Evidence{Metric: MetricWindow{Samples: 10, Average: 200}, PriorMetric: MetricWindow{Samples: 10, Average: 53}}, true, true},
		{"metric rose too far", rise, Evidence{Metric: MetricWindow{Samples: 10, Average: 400}, PriorMetric: MetricWindow{Samples: 10, Average: 200}}, true, false},
		{"negative baseline compares by magnitude", rise, Evidence{Metric: MetricWindow{Samples: 10, Average: -5}, PriorMetric: MetricWindow{Samples: 10, Average: -20}}, true, false},
		{"zero baseline is not judged", rise, Evidence{Metric: MetricWindow{Samples: 10, Average: 5}, PriorMetric: MetricWindow{Samples: 10, Average: 0}}, false, false},
		{"too few earlier values", drop, Evidence{Metric: MetricWindow{Samples: 10, Average: 1}, PriorMetric: MetricWindow{Samples: 2, Average: 53}}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := Judge([]Gate{tc.gate}, map[string]Evidence{tc.gate.Name: tc.ev})
			if judged := len(v.Results) == 1; judged != tc.wantJudged {
				t.Fatalf("judged = %v, want %v (%+v)", judged, tc.wantJudged, v)
			}
			if !tc.wantJudged {
				return
			}
			r := v.Results[0]
			if r.Passed != tc.wantPassed || r.Summary == "" {
				t.Fatalf("result = %+v, want passed %v", r, tc.wantPassed)
			}
			decision := api.RolloutDecision{Kind: api.RolloutDecisionAbort, Rule: api.RolloutRuleAbortReleaseGate,
				Summary: r.Summary, DecidedAt: time.Now().UTC().Format(time.RFC3339Nano), Gates: []api.ReleaseGateResult{r}}
			if err := decision.Validate(); err != nil {
				t.Fatalf("result does not fit a rollout decision: %v", err)
			}
		})
	}
	v := Judge([]Gate{synthetic}, map[string]Evidence{synthetic.Name: {Probes: ProbeCounts{Requests: 8, Failures: 5}, BaselineProbes: ProbeCounts{Requests: 8}}})
	if got := v.Summary(); got != "release gate checkout-probe: checkout failed 5 of 8 probes (62%, max 10%) vs 0% on the stable revision" {
		t.Fatalf("summary = %q", got)
	}
	v = Judge([]Gate{drop}, map[string]Evidence{drop.Name: {Metric: MetricWindow{Samples: 10, Average: 31.2}, PriorMetric: MetricWindow{Samples: 10, Average: 52.9}}})
	if v.Failed == nil || v.Failed.Observed >= -30 || v.Failed.Bound != -30 || v.Failed.Baseline != 52.9 {
		t.Fatalf("metric change result = %+v", v.Failed)
	}
	if got := v.Summary(); got != "release gate orders-drop: orders fell 41% (31.2 vs 52.9 before the rollout; fails below -30%)" {
		t.Fatalf("summary = %q", got)
	}
}

func TestFromRequestSyntheticAndMetricChange(t *testing.T) {
	maxFailure := 25
	threshold := 3.0
	for _, tc := range []struct {
		name    string
		req     api.CreateReleaseGateRequest
		wantErr bool
	}{
		{"synthetic with defaults", api.CreateReleaseGateRequest{Name: "probe", Kind: KindSynthetic, Check: "checkout"}, false},
		{"synthetic with a bound", api.CreateReleaseGateRequest{Name: "probe", Kind: KindSynthetic, Check: "checkout", MaxFailurePercent: &maxFailure}, false},
		{"synthetic without a check", api.CreateReleaseGateRequest{Name: "probe", Kind: KindSynthetic}, true},
		{"synthetic with a metric", api.CreateReleaseGateRequest{Name: "probe", Kind: KindSynthetic, Check: "checkout", Metric: "orders"}, true},
		{"metric change", api.CreateReleaseGateRequest{Name: "drop", Kind: KindMetricChange, Metric: "orders", Comparison: "below", MaxChangePercent: 30}, false},
		{"metric change without a bound", api.CreateReleaseGateRequest{Name: "drop", Kind: KindMetricChange, Metric: "orders", Comparison: "below"}, true},
		{"metric change bound too large", api.CreateReleaseGateRequest{Name: "drop", Kind: KindMetricChange, Metric: "orders", Comparison: "below", MaxChangePercent: 5000}, true},
		{"metric change with a threshold", api.CreateReleaseGateRequest{Name: "drop", Kind: KindMetricChange, Metric: "orders", Comparison: "below", MaxChangePercent: 30, Threshold: &threshold}, true},
		{"custom metric with a change bound", api.CreateReleaseGateRequest{Name: "m", Kind: KindCustomMetric, Metric: "orders", Comparison: "below", Threshold: &threshold, MaxChangePercent: 30}, true},
		{"slo with a check", api.CreateReleaseGateRequest{Name: "s", Kind: KindSLO, SLO: "checkout", Check: "checkout"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec, p := FromRequest(tc.req)
			if (p != nil) != tc.wantErr {
				t.Fatalf("problem = %v, wantErr %v", p, tc.wantErr)
			}
			if tc.name == "synthetic with defaults" && (spec.MaxFailurePercent != api.ReleaseGateDefaultMaxFailurePercent ||
				spec.MinSamples != api.ReleaseGateDefaultSyntheticMinSamples || spec.CheckName != "checkout") {
				t.Fatalf("spec = %+v", spec)
			}
		})
	}
}
