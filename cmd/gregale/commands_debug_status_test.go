package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestRenderDebugStatus(t *testing.T) {
	resp := api.DebugStatusResponse{
		Verdict: api.DebugStatusRollback, Headline: "Rolled back at 10% traffic: 5xx regression.", Since: "1h",
		Health: api.DebugStatusHealth{Requests: 120, ServerErrors: 3, P95MS: 85},
		Deployments: []api.DebugStatusDeployment{
			{ID: "3f2a9c41-0000-4000-8000-000000000001", TrafficPercent: 100, CommitSHA: "a1b2c3d4e5f6"},
		},
		Rollout:          &api.DebugRolloutExplanation{State: api.DebugRolloutAborted, Headline: "Rolled back at 10% traffic: 5xx regression."},
		Regressions:      []api.DebugRegressionItem{{Route: "/checkout", Kind: "errors", ErrorRatePct: 95.2, DeploymentID: "7d1e2f3a-0000-4000-8000-000000000002"}},
		RegressionsTotal: 3,
		RecentFailures:   []api.DebugTelemetryRequestItem{{RequestID: "req_8f2", Method: "GET", Route: "GET /checkout", Status: 500, ReceivedAt: "2026-10-22T12:04:31.118Z"}},
	}
	var out bytes.Buffer
	renderDebugStatus(&out, "shop", resp)
	got := out.String()
	for _, want := range []string{
		"shop: rolled back\n  Rolled back at 10% traffic: 5xx regression.",
		"120 requests, 3 server errors, p95 85 ms",
		"3f2a9c41  100%  commit a1b2c3d4",
		"/checkout  errors 95.2% (was 0.0%)  deployment 7d1e2f3a",
		"... and 2 more",
		"12:04:31  GET /checkout  500  req_8f2",
		"off (set tracing.enabled: true",
		"Next:\n  gregale debug rollout shop\n  gregale debug requests inspect shop req_8f2\n  gregale debug regressions shop\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("status output missing %q:\n%s", want, got)
		}
	}
}

func TestRenderDebugRollout(t *testing.T) {
	resp := api.DebugRolloutExplanation{
		DeploymentID: "7d1e2f3a-0000-4000-8000-000000000002", StableDeploymentID: "3f2a9c41-0000-4000-8000-000000000001",
		State: api.DebugRolloutAborted, Headline: "Rolled back at 10% traffic: 5xx regression.",
		WindowStart: "2026-10-22T11:40:00Z", WindowEnd: "2026-10-22T11:45:00Z", RetentionClamped: true,
		Decision: &api.RolloutDecision{Kind: api.RolloutDecisionAbort, Rule: api.RolloutRuleAbort5xx, Step: 0, TrafficPercent: 10},
		Routes:   []api.DebugRolloutRoute{{Route: "/checkout", CandidateRequests: 21, CandidateServerErrors: 20, CandidateP95MS: 90, StableRequests: 21, StableP95MS: 80}},
		Examples: []api.DebugTelemetryRequestItem{{ID: "row-1", Method: "GET", Route: "/checkout", Status: 500, LatencyMS: 12, ReceivedAt: "2026-10-22T11:44:01Z"}},
		Regressions: []api.DebugRegressionItem{{Route: "/checkout", P95MS: 900, P95BaseMS: 200,
			SuspectedDependency: &api.DebugSuspectedDependency{Name: "postgres SELECT orders"}}},
	}
	var out bytes.Buffer
	renderDebugRollout(&out, "shop", resp)
	got := out.String()
	for _, want := range []string{
		"Rolled back at 10% traffic: 5xx regression.\nDeployment 7d1e2f3a-0000-4000-8000-000000000002 vs stable 3f2a9c41",
		"(shortened to the plan's debugger retention)",
		"Decision abort_5xx (abort) at step 1, 10% traffic",
		"/checkout  21        20             90 ms  0/21           80 ms",
		"11:44:01  GET /checkout  500  12 ms  gregale debug requests inspect shop row-1",
		"/checkout  p95 900 ms (was 200 ms)  suspected: postgres SELECT orders",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rollout output missing %q:\n%s", want, got)
		}
	}

	out.Reset()
	renderDebugRollout(&out, "shop", api.DebugRolloutExplanation{State: api.DebugRolloutNone, Headline: "No canary rollout among the app's recent deployments."})
	if out.String() != "No canary rollout among the app's recent deployments.\n" {
		t.Fatalf("no-rollout output = %q", out.String())
	}
}
