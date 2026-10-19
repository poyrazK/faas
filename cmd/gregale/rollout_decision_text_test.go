package main_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestDescribeRolloutDecision(t *testing.T) {
	cases := []struct {
		decision api.RolloutDecision
		want     []string
	}{
		{api.RolloutDecision{Rule: api.RolloutRuleHoldInsufficient, Summary: "insufficient request samples (candidate=3 stable=41; need 20 each)",
			Candidate: &api.RolloutDecisionSample{Requests: 3, ProbeRequests: 2}, Stable: &api.RolloutDecisionSample{Requests: 41}},
			[]string{"waiting for enough traffic", "this release 3 requests", "the current one 41", "20 each needed", "probes 2 and 0"}},
		{api.RolloutDecision{Rule: api.RolloutRuleAdvanceProbeEvidence,
			Candidate: &api.RolloutDecisionSample{ProbeRequests: 30}, Stable: &api.RolloutDecisionSample{ProbeRequests: 21}},
			[]string{"synthetic probes", "0/30 probe 5xx", "0/21"}},
		{api.RolloutDecision{Rule: api.RolloutRuleAdvanceHealthy,
			Candidate: &api.RolloutDecisionSample{Requests: 52, P95LatencyMS: 120}, Stable: &api.RolloutDecisionSample{Requests: 410, P95LatencyMS: 110}},
			[]string{"healthy", "0/52 5xx", "0/410", "p95 120 ms vs 110 ms"}},
		{api.RolloutDecision{Rule: api.RolloutRuleAbort5xx, ProbesIncluded: true, Summary: "5xx regression (candidate 66.7%, stable 0.0%; 20/30 vs 0/30 requests)"},
			[]string{"5xx regression", "20/30", "(synthetic probes included)"}},
		{api.RolloutDecision{Rule: api.RolloutRuleAbortManual}, []string{"aborted on request"}},
		{api.RolloutDecision{Rule: api.RolloutRuleHoldSignalUnavailable, Summary: "liveness restart signal unavailable"}, []string{"liveness restart signal unavailable"}},
	}
	for _, tc := range cases {
		got := main.DescribeRolloutDecision(&tc.decision)
		for _, want := range tc.want {
			if !strings.Contains(got, want) {
				t.Errorf("%s: %q does not contain %q", tc.decision.Rule, got, want)
			}
		}
	}
}

// TestRolloutHeldNoticeUsesRecordedHold — ADR-1447: the wait prints the
// reason meterd recorded, once per step and rule, instead of a generic guess.
func TestRolloutHeldNoticeUsesRecordedHold(t *testing.T) {
	now := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	started := now.Add(-time.Minute)
	hold := &api.RolloutDecision{Kind: api.RolloutDecisionHold, Rule: api.RolloutRuleHoldInsufficient,
		Candidate: &api.RolloutDecisionSample{Requests: 3}, Stable: &api.RolloutDecisionSample{Requests: 41}}
	d := api.DeploymentResponse{ID: "dep-1", Status: main.StatusLive, RolloutState: "rolling_out", CanaryStep: 0, CanaryTotalSteps: 4,
		TrafficPercent: 1, CanaryStepStartedAt: &started, RolloutHold: hold}
	var out bytes.Buffer
	var n main.RolloutHeldNotice
	n.MaybeWarn(&out, d, now)
	n.MaybeWarn(&out, d, now.Add(10*time.Minute))
	if got := out.String(); strings.Count(got, "Rollout held") != 1 || !strings.Contains(got, "1% traffic (step 1/4): waiting for enough traffic") {
		t.Fatalf("recorded hold notice:\n%s", got)
	}
	d.RolloutHold = &api.RolloutDecision{Kind: api.RolloutDecisionHold, Rule: api.RolloutRuleHoldHealthAlert, Summary: "1 safe-release health alert(s) with a rollback action firing (metric error_rate)"}
	n.MaybeWarn(&out, d, now.Add(11*time.Minute))
	if got := out.String(); strings.Count(got, "Rollout held") != 2 || !strings.Contains(got, "health alert") {
		t.Fatalf("changed hold reason not printed:\n%s", got)
	}
}

func TestRenderDeploymentProgressExplainsAdvanceAndAbort(t *testing.T) {
	var out bytes.Buffer
	d := api.DeploymentResponse{Status: main.StatusLive, RolloutState: "rolling_out", CanaryStep: 0, CanaryTotalSteps: 4, TrafficPercent: 1}
	state := main.RenderDeploymentProgress(&out, d, nil)
	d.CanaryStep, d.TrafficPercent = 1, 10
	d.RolloutDecision = &api.RolloutDecision{Kind: api.RolloutDecisionAdvance, Rule: api.RolloutRuleAdvanceProbeEvidence, DecidedAt: "2026-10-14T09:00:00Z",
		Candidate: &api.RolloutDecisionSample{ProbeRequests: 30}, Stable: &api.RolloutDecisionSample{ProbeRequests: 21}}
	state = main.RenderDeploymentProgress(&out, d, state)
	if got := out.String(); !strings.Contains(got, "10% traffic · step 2/4 · rolling_out · advanced on synthetic probes") {
		t.Fatalf("advance line:\n%s", got)
	}
	d.RolloutState, d.TrafficPercent, d.RolloutAbortedReason = main.RolloutStateAborted, 0, "circuit breaker: 5xx error rate regression"
	d.RolloutDecision = &api.RolloutDecision{Kind: api.RolloutDecisionAbort, Rule: api.RolloutRuleAbort5xx, DecidedAt: "2026-10-14T09:05:00Z",
		Summary: "5xx regression (candidate 66.7%, stable 0.0%; 20/30 vs 0/30 requests)"}
	main.RenderDeploymentProgress(&out, d, state)
	if got := out.String(); !strings.Contains(got, "5xx error rate regression · 5xx regression (candidate 66.7%") {
		t.Fatalf("abort line:\n%s", got)
	}
}

func TestRenderRolloutDecisionsInSummary(t *testing.T) {
	var out bytes.Buffer
	main.RenderRolloutDecisions(&out, api.DeploymentResponse{Status: main.StatusLive, RolloutState: "rolling_out", CanaryStep: 1, CanaryTotalSteps: 4, TrafficPercent: 10,
		RolloutHold:     &api.RolloutDecision{Rule: api.RolloutRuleHoldObservation, DecidedAt: "2026-10-14T09:06:00Z"},
		RolloutDecision: &api.RolloutDecision{Rule: api.RolloutRuleAdvanceManual, DecidedAt: "2026-10-14T09:01:00Z"}})
	got := out.String()
	for _, want := range []string{"rollout: rolling_out at 10% traffic (step 2/4)", "held since 2026-10-14T09:06:00Z: health data is unavailable", "last decision (advance_manual, 2026-10-14T09:01:00Z): advanced on request"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary missing %q:\n%s", want, got)
		}
	}
	out.Reset()
	main.RenderRolloutDecisions(&out, api.DeploymentResponse{Status: main.StatusLive})
	if out.Len() != 0 {
		t.Fatalf("summary printed a rollout section without a canary: %s", out.String())
	}
}
