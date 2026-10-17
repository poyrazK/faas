package release

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/canary"
)

func TestJudgePostRelease(t *testing.T) {
	healthy := canary.HealthWindow{Requests: 400, ServerErrors: 2, P95LatencyMS: 40}
	for _, tc := range []struct {
		name        string
		release     canary.HealthWindow
		predecessor canary.HealthWindow
		wantRule    string
	}{
		{"healthy release", canary.HealthWindow{Requests: 300, ServerErrors: 1, P95LatencyMS: 45}, healthy, ""},
		{"5xx regression", canary.HealthWindow{Requests: 300, ServerErrors: 60, P95LatencyMS: 45}, healthy, api.RolloutRuleAbortPostRelease5xx},
		{"p95 regression", canary.HealthWindow{Requests: 300, ServerErrors: 1, P95LatencyMS: 700}, healthy, api.RolloutRuleAbortPostReleaseP95},
		{"slower but within 100ms", canary.HealthWindow{Requests: 300, P95LatencyMS: 130}, healthy, ""},
		{"too few release requests", canary.HealthWindow{Requests: 10, ServerErrors: 2, P95LatencyMS: 900}, healthy, ""},
		{"quiet predecessor, failing release", canary.HealthWindow{Requests: 8, ServerErrors: 8}, canary.HealthWindow{Requests: 3}, api.RolloutRuleAbortPostRelease5xx},
		{"quiet predecessor, slow release is not comparable", canary.HealthWindow{Requests: 80, P95LatencyMS: 900}, canary.HealthWindow{Requests: 3, P95LatencyMS: 30}, ""},
		{"predecessor already failing", canary.HealthWindow{Requests: 300, ServerErrors: 150}, canary.HealthWindow{Requests: 300, ServerErrors: 140}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rule, reason, regressed := JudgePostRelease(tc.release, tc.predecessor)
			if rule != tc.wantRule || regressed != (tc.wantRule != "") || (regressed && reason == "") {
				t.Fatalf("JudgePostRelease = %q %q %v, want rule %q", rule, reason, regressed, tc.wantRule)
			}
		})
	}
}

func TestPostReleaseDecisionValidates(t *testing.T) {
	now := time.Now()
	release := canary.HealthWindow{Requests: 300, ServerErrors: 1, P95LatencyMS: 700}
	predecessor := canary.HealthWindow{Requests: 400, ServerErrors: 2, P95LatencyMS: 40}
	rule, reason, _ := JudgePostRelease(release, predecessor)
	d := PostReleaseDecision(rule, reason, uuid.NewString(), release, predecessor, now.Add(-10*time.Minute), now, now)
	if err := d.Validate(); err != nil {
		t.Fatalf("decision %+v invalid: %v", d, err)
	}
	if d.Kind != api.RolloutDecisionAbort || d.TrafficPercent != 100 || d.Candidate.P95LatencyMS != 700 || d.Stable.Requests != 400 {
		t.Fatalf("decision = %+v", d)
	}
}
