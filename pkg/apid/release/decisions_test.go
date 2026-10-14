package release

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/api/canary"
)

// TestStageDwell pins ADR-1321: only conclusive, clean evidence on a stage
// paced by evidence shortens the dwell APID enforces for the worker.
func TestStageDwell(t *testing.T) {
	paced := canary.Stage{Percent: 1, MinDuration: time.Minute, Duration: 2 * time.Minute}
	timed := canary.Stage{Percent: 1, Duration: 2 * time.Minute}
	advance := func(rule string) *api.RolloutDecision {
		return &api.RolloutDecision{Kind: api.RolloutDecisionAdvance, Rule: rule}
	}
	for _, tc := range []struct {
		name     string
		stage    canary.Stage
		decision *api.RolloutDecision
		want     time.Duration
	}{
		{"healthy evidence on a paced stage", paced, advance(api.RolloutRuleAdvanceHealthy), time.Minute},
		{"probe evidence on a paced stage", paced, advance(api.RolloutRuleAdvanceProbeEvidence), time.Minute},
		{"no-news advance keeps the timer", paced, advance(api.RolloutRuleAdvanceLowTraffic), 2 * time.Minute},
		{"no predecessor keeps the timer", paced, advance(api.RolloutRuleAdvanceNoPredecessor), 2 * time.Minute},
		{"an older worker without a record keeps the timer", paced, nil, 2 * time.Minute},
		{"a timer-only stage ignores evidence", timed, advance(api.RolloutRuleAdvanceHealthy), 2 * time.Minute},
	} {
		if got := StageDwell(tc.stage, tc.decision); got != tc.want {
			t.Errorf("%s: StageDwell = %v, want %v", tc.name, got, tc.want)
		}
	}
}
