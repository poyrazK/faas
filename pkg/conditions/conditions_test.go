package conditions_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/conditions"
)

var observed = time.Date(2026, 10, 30, 15, 0, 0, 0, time.UTC)

type want struct {
	status, reason string
	terminal       bool
}

func assertConditions(t *testing.T, got api.ResourceStatus, kind string, wants map[string]want) {
	t.Helper()
	types := api.ConditionTypes[kind]
	if len(got.Conditions) != len(types) {
		t.Fatalf("got %d conditions, want %v", len(got.Conditions), types)
	}
	for i, c := range got.Conditions {
		if c.Type != types[i] {
			t.Errorf("condition %d is %s, want %s", i, c.Type, types[i])
		}
		w, ok := wants[c.Type]
		if !ok {
			continue
		}
		if c.Status != w.status || c.Reason != w.reason || c.Terminal != w.terminal {
			t.Errorf("%s = %s/%s terminal=%v, want %s/%s terminal=%v (%q)", c.Type, c.Status, c.Reason, c.Terminal, w.status, w.reason, w.terminal, c.Message)
		}
		if c.Message == "" {
			t.Errorf("%s has no message", c.Type)
		}
	}
}

func health(status, phase string, serving []string, checks ...api.AppHealthCheck) api.AppHealthResponse {
	return api.AppHealthResponse{Status: status, Phase: phase, Summary: "summary", ServingDeploymentIDs: serving, Checks: checks}
}

func chk(code, status string) api.AppHealthCheck {
	return api.AppHealthCheck{Code: code, Status: status, Detail: code + " " + status, DeploymentID: "d2"}
}

func TestApp(t *testing.T) {
	serving := []string{"d1"}
	tests := []struct {
		name   string
		health api.AppHealthResponse
		wants  map[string]want
	}{
		{"serving", health("healthy", "serving", serving, chk("deployment", "pass"), chk("readiness", "pass")), map[string]want{
			"Ready": {"True", "serving", false}, "Progressing": {"False", "complete", false}, "Degraded": {"False", "healthy", false}}},
		{"idle scale to zero is ready", health("healthy", "idle", serving, chk("readiness", "pass")), map[string]want{
			"Ready": {"True", "idle", false}}},
		{"degraded stays ready", health("degraded", "serving", serving, chk("readiness", "warning")), map[string]want{
			"Ready": {"True", "serving", false}, "Degraded": {"True", "readiness", false}}},
		{"unhealthy", health("unhealthy", "serving", serving, chk("deployment", "pass"), chk("readiness", "fail")), map[string]want{
			"Ready": {"False", "readiness", false}, "Degraded": {"True", "readiness", false}}},
		{"unknown evidence", health("unknown", "serving", serving, chk("readiness", "unknown")), map[string]want{
			"Ready": {"Unknown", "readiness", false}, "Degraded": {"Unknown", "readiness", false}}},
		{"first deploy in progress", health("unknown", "deploying", nil, chk("latest_deployment", "not_applicable"), chk("deployment", "unknown")), map[string]want{
			"Ready": {"False", "deploying", false}, "Progressing": {"True", "deploying", false}}},
		{"deploy in progress beside a serving release", health("healthy", "deploying", serving, chk("latest_deployment", "not_applicable"), chk("readiness", "pass")), map[string]want{
			"Ready": {"True", "serving", false}, "Progressing": {"True", "deploying", false}}},
		{"only deployment failed", health("unhealthy", "not_deployed", nil, chk("latest_deployment", "fail"), chk("deployment", "unknown")), map[string]want{
			"Ready": {"False", "not_deployed", true}, "Progressing": {"False", "latest_deployment_failed", true}}},
		{"failed deploy beside a serving release", health("degraded", "serving", serving, chk("latest_deployment", "warning"), chk("readiness", "pass")), map[string]want{
			"Ready": {"True", "serving", false}, "Progressing": {"False", "latest_deployment_failed", true}, "Degraded": {"True", "latest_deployment", false}}},
		{"maintenance", health("degraded", "maintenance", serving, chk("maintenance", "warning")), map[string]want{
			"Ready": {"False", "maintenance", false}}},
		{"stopped service", health("healthy", "stopped", serving, chk("readiness", "not_applicable")), map[string]want{
			"Ready": {"False", "stopped", true}}},
		{"idle without request telemetry is ready", health("unknown", "idle", serving, chk("deployment", "pass"), chk("readiness", "pass"), chk("requests", "unknown")), map[string]want{
			"Ready": {"True", "idle", false}, "Degraded": {"Unknown", "requests", false}}},
		{"failing requests degrade a ready app", health("unhealthy", "serving", serving, chk("readiness", "pass"), chk("requests", "fail")), map[string]want{
			"Ready": {"True", "serving", false}, "Degraded": {"True", "requests", false}}},
		{"worker", health("unknown", "unsupported", nil, chk("workload", "unknown")), map[string]want{
			"Ready": {"Unknown", "not_assessed", false}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := conditions.App("api", tc.health, observed)
			if got.Kind != "app" || got.Name != "api" || got.ObservedAt != "2026-10-30T15:00:00Z" {
				t.Errorf("header = %+v", got)
			}
			assertConditions(t, got, api.ResourceKindApp, tc.wants)
		})
	}
}

func TestDeployment(t *testing.T) {
	completedAt := observed.Add(-time.Minute)
	readiness := func(status string) json.RawMessage {
		return json.RawMessage(`{"history":[{"name":"build","status":"completed"},{"name":"readiness","status":"` + status + `"}]}`)
	}
	tests := []struct {
		name  string
		d     api.DeploymentResponse
		wants map[string]want
		since string
	}{
		{"building", api.DeploymentResponse{Status: "building"}, map[string]want{
			"Ready": {"False", "building", false}, "Progressing": {"True", "building", false}, "RolledOut": {"False", "building", false}}, ""},
		{"live without stages", api.DeploymentResponse{Status: "live", TrafficPercent: 100}, map[string]want{
			"Ready": {"True", "live", false}, "Progressing": {"False", "complete", false}, "RolledOut": {"True", "rolled_out", false}}, ""},
		{"live while readiness runs", api.DeploymentResponse{Status: "live", TrafficPercent: 100, StageState: readiness("running")}, map[string]want{
			"Ready": {"False", "verifying", false}, "Progressing": {"True", "verifying", false}}, ""},
		{"live after readiness", api.DeploymentResponse{Status: "live", TrafficPercent: 100, StageState: readiness("completed")}, map[string]want{
			"Ready": {"True", "live", false}}, ""},
		{"rolling out", api.DeploymentResponse{Status: "live", TrafficPercent: 5, CanaryTotalSteps: 3, CanaryStep: 1, RolloutState: "rolling_out"}, map[string]want{
			"Ready": {"True", "live", false}, "Progressing": {"True", "rolling_out", false}, "RolledOut": {"False", "rolling_out", false}}, ""},
		{"rolled out", api.DeploymentResponse{Status: "live", TrafficPercent: 100, CanaryTotalSteps: 3, RolloutState: "complete", RolloutCompletedAt: &completedAt}, map[string]want{
			"Progressing": {"False", "complete", false}, "RolledOut": {"True", "rolled_out", false}}, "2026-10-30T14:59:00Z"},
		{"traffic split share", api.DeploymentResponse{Status: "live", TrafficPercent: 30, RolloutState: "complete", RolloutCompletedAt: &completedAt}, map[string]want{
			"RolledOut": {"True", "rolled_out", false}}, "2026-10-30T14:59:00Z"},
		// deploy --no-traffic: ready on its own URL, not rolled out until promoted.
		{"staged with no traffic", api.DeploymentResponse{Status: "live", TrafficPercent: 0, RolloutState: "complete", RolloutCompletedAt: &completedAt}, map[string]want{
			"Ready": {"True", "live", false}, "Progressing": {"False", "complete", false}, "RolledOut": {"False", "staged", false}}, ""},
		{"staged in the default scope", api.DeploymentResponse{Status: "live", Scope: "default", TrafficPercent: 0, RolloutState: "complete"}, map[string]want{
			"RolledOut": {"False", "staged", false}}, ""},
		// A project environment serves its release set's members at 0%.
		{"release set member", api.DeploymentResponse{Status: "live", Scope: "staging", TrafficPercent: 0, RolloutState: "complete", RolloutCompletedAt: &completedAt}, map[string]want{
			"RolledOut": {"True", "rolled_out", false}}, "2026-10-30T14:59:00Z"},
		{"production release set member", api.DeploymentResponse{Status: "live", Scope: "production", TrafficPercent: 0, RolloutState: "complete", RolloutCompletedAt: &completedAt}, map[string]want{
			"RolledOut": {"True", "rolled_out", false}}, "2026-10-30T14:59:00Z"},
		{"rollout aborted", api.DeploymentResponse{Status: "live", CanaryTotalSteps: 3, RolloutState: "aborted", RolloutAbortedReason: "error rate 9%"}, map[string]want{
			"RolledOut": {"False", "rollout_aborted", true}}, ""},
		{"failed", api.DeploymentResponse{Status: "failed", Error: "build exited 1"}, map[string]want{
			"Ready": {"False", "failed", true}, "Progressing": {"False", "failed", true}, "RolledOut": {"False", "failed", true}}, ""},
		{"superseded", api.DeploymentResponse{Status: "superseded"}, map[string]want{
			"Ready": {"False", "superseded", true}}, ""},
		{"cancelled", api.DeploymentResponse{Status: "cancelled"}, map[string]want{
			"Ready": {"False", "cancelled", true}}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.d.ID = "d1"
			got := conditions.Deployment(tc.d, observed)
			if got.Kind != "deployment" || got.Name != "d1" {
				t.Errorf("header = %+v", got)
			}
			assertConditions(t, got, api.ResourceKindDeployment, tc.wants)
			if since := got.Conditions[2].Since; since != tc.since {
				t.Errorf("RolledOut since = %q, want %q", since, tc.since)
			}
		})
	}
}

// A failed deployment's message carries its error; a terminal condition
// always says why.
func TestDeploymentFailedMessage(t *testing.T) {
	got := conditions.Deployment(api.DeploymentResponse{ID: "d1", Status: "failed", Error: "build exited 1"}, observed)
	if msg := got.Conditions[0].Message; msg != "The deployment failed: build exited 1" {
		t.Errorf("Ready message = %q", msg)
	}
}
