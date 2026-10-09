// adr: 830
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAutomationFailurePolicyAPI(t *testing.T) {
	e := setup(t, api.PlanHobby)
	app := seedWorkflowApp(t, e, "failure-policy")
	raw := json.RawMessage(`[{"name":"guard","trigger":{"type":"event","source":"billing","event_type":"invoice.paid"},"steps":[{"name":"main","path":"/report"}]}]`)
	if _, err := e.store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive, ImageDigest: "sha256:x", Workflows: raw}); err != nil {
		t.Fatal(err)
	}
	base := "/v1/apps/" + app.Slug + "/automations/guard"
	path := base + "/failure-policy"
	enabled := true
	req := api.SetAutomationFailurePolicyRequest{Enabled: &enabled, FailureThreshold: 1, MinCompletedRuns: 1, WindowSeconds: 300}
	rec := e.do(t, "GET", path, nil, nil)
	var out api.AutomationFailurePolicyResponse
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.Policy.Enabled || out.Policy.Version != 0 {
		t.Fatalf("default %d %s", rec.Code, rec.Body)
	}
	rec = e.do(t, "PUT", path, req, nil)
	if rec.Code != 200 {
		t.Fatalf("configure %d %s", rec.Code, rec.Body)
	}
	rec = e.do(t, "PUT", path, req, nil)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "automation_failure_policy_conflict") {
		t.Fatalf("CAS %d %s", rec.Code, rec.Body)
	}
	req.ExpectedVersion = 1
	req.Enabled = nil
	rec = e.do(t, "PUT", path, req, nil)
	if rec.Code != 422 {
		t.Fatalf("missing enabled %d %s", rec.Code, rec.Body)
	}
	run := &state.WorkflowRun{AppID: app.ID, WorkflowName: "guard", DefinitionSnapshot: json.RawMessage(`{"name":"guard","steps":[{"name":"main","path":"/report"}]}`), Input: json.RawMessage(`{"secret":"private-input"}`)}
	if err := e.store.CreateWorkflowRun(t.Context(), run); err != nil {
		t.Fatal(err)
	}
	private := "private-executor-error"
	if err := e.store.MarkWorkflowRunStatus(t.Context(), run.ID, state.WorkflowRunStatusFailed, nil, &private); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.EvaluateAutomationFailurePolicies(t.Context(), "", "", 100, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	rec = e.do(t, "GET", path, nil, nil)
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || !out.Paused || out.Generation != 1 || rec.Header().Get("Cache-Control") != "no-store" || strings.Contains(rec.Body.String(), "private-") {
		t.Fatalf("pause %d %s", rec.Code, rec.Body)
	}
	rec = e.do(t, "GET", base, nil, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"failure_paused":true`) {
		t.Fatalf("invisible pause %d %s", rec.Code, rec.Body)
	}
	rec = e.do(t, "POST", path+"/resume", api.ResumeAutomationFailurePauseRequest{ExpectedGeneration: 2}, nil)
	if rec.Code != 409 {
		t.Fatalf("stale resume %d %s", rec.Code, rec.Body)
	}
	rec = e.do(t, "POST", path+"/resume", api.ResumeAutomationFailurePauseRequest{ExpectedGeneration: 1}, nil)
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.Paused || out.Generation != 2 || out.ObservedFailures != 0 {
		t.Fatalf("resume %d %s", rec.Code, rec.Body)
	}
	key, hash, _ := api.GenerateAPIKey()
	if _, err := e.store.CreateAPIKey(context.Background(), e.acct.ID, hash, "read-failure-policy", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	e.key = key
	if rec = e.do(t, "GET", path, nil, nil); rec.Code != 200 {
		t.Fatalf("read scope %d", rec.Code)
	}
	req.Enabled = &enabled
	if rec = e.do(t, "PUT", path, req, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("read key wrote %d", rec.Code)
	}
	if rec = e.do(t, "POST", path+"/resume", api.ResumeAutomationFailurePauseRequest{ExpectedGeneration: 1}, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("read key resumed %d", rec.Code)
	}
}
