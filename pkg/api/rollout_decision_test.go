package api

import (
	"math"
	"strings"
	"testing"
	"time"
)

func validRolloutDecision() RolloutDecision {
	at := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	return RolloutDecision{
		Kind: RolloutDecisionAdvance, Rule: RolloutRuleAdvanceProbeEvidence,
		Summary: "insufficient request samples (candidate=0 stable=3; need 20 each)",
		Step:    0, TrafficPercent: 1,
		WindowStart: at.Add(-time.Minute).Format(time.RFC3339Nano), WindowEnd: at.Format(time.RFC3339Nano),
		StableDeploymentID: "6f1c2c3e-4c1b-4f61-9a0e-6a3f5c2d1b00",
		Candidate:          &RolloutDecisionSample{ProbeRequests: 30},
		Stable:             &RolloutDecisionSample{Requests: 3, ProbeRequests: 21},
		ProbesIncluded:     true,
		Signals:            &RolloutDecisionSignals{},
		DecidedAt:          at.Format(time.RFC3339Nano),
	}
}

func TestRolloutDecisionValidate(t *testing.T) {
	if err := validRolloutDecision().Validate(); err != nil {
		t.Fatalf("valid decision rejected: %v", err)
	}
	cases := []struct {
		name   string
		mutate func(*RolloutDecision)
	}{
		{"unknown rule", func(d *RolloutDecision) { d.Rule = "advance_because" }},
		{"rule of another kind", func(d *RolloutDecision) { d.Kind = RolloutDecisionAbort }},
		{"control character in summary", func(d *RolloutDecision) { d.Summary = "held\nfor ever" }},
		{"oversized summary", func(d *RolloutDecision) { d.Summary = strings.Repeat("x", RolloutDecisionSummaryMaxBytes+1) }},
		{"traffic above 100", func(d *RolloutDecision) { d.TrafficPercent = 101 }},
		{"negative step", func(d *RolloutDecision) { d.Step = -1 }},
		{"missing decided_at", func(d *RolloutDecision) { d.DecidedAt = "" }},
		{"bad window", func(d *RolloutDecision) { d.WindowEnd = "yesterday" }},
		{"stable id not a uuid", func(d *RolloutDecision) { d.StableDeploymentID = "../apps" }},
		{"negative sample", func(d *RolloutDecision) { d.Candidate.ServerErrors = -1 }},
		{"non-finite latency", func(d *RolloutDecision) { d.Stable.P95LatencyMS = math.Inf(1) }},
		{"negative signal", func(d *RolloutDecision) { d.Signals.OOMKills = -1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := validRolloutDecision()
			tc.mutate(&d)
			if err := d.Validate(); err == nil {
				t.Fatal("invalid decision accepted")
			}
		})
	}
}

func TestRolloutDecisionRulesHaveKinds(t *testing.T) {
	for rule, kind := range rolloutDecisionRules {
		if !strings.HasPrefix(rule, kind+"_") {
			t.Errorf("rule %q is filed under kind %q", rule, kind)
		}
	}
}

func TestManualRolloutDecisionIsValid(t *testing.T) {
	for _, rule := range []string{RolloutRuleAdvanceManual, RolloutRuleAbortManual, RolloutRuleAbortLeaseExpired} {
		if err := ManualRolloutDecision(rule, "aborted by a person", 1, 10, time.Now()).Validate(); err != nil {
			t.Errorf("%s: %v", rule, err)
		}
	}
}
