package main

import (
	"context"
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestWorkflowRecoveryPolicyEvaluationHTTP(t *testing.T) {
	e := setup(t, api.PlanHobby)
	appID := mustSeedApp(t, e, "evaluate")
	path := "/v1/apps/evaluate/recovery-policy"
	req := api.WorkflowRecoveryPolicyEvaluationRequest{Policy: api.WorkflowRecoveryPolicy{Enabled: true, Mode: "preview", WorkflowNames: []string{"orders"}, Triggers: []string{"saved_failure"}, MaxResumeCount: 3, CooldownSeconds: 60, HourlyActionBudget: 10, RatePerSecond: 1, Reason: "test historical settings"}}
	clear, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.store.CreateAPIKey(context.Background(), e.acct.ID, hash, "read-evaluation", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	e.key = clear
	rec := e.do(t, http.MethodPost, path+"/evaluate", req, nil)
	var out api.WorkflowRecoveryPolicyEvaluation
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.Current.Version != 0 || out.FailureCoverage.CaptureStartedAt.IsZero() || out.FailureCoverage.Coverage != "partial" || out.Candidates == nil || len(out.CoverageNotes) < 6 || out.Proposed.FailureStatuses == nil || out.Current.Policy.FailureStatuses == nil || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("evaluate %d %s", rec.Code, rec.Body.String())
	}
	snapshot, _ := json.Marshal(api.WorkflowSpec{Name: "orders", Steps: []api.WorkflowStepSpec{{Name: "send", Run: "send"}}})
	run := &state.WorkflowRun{AppID: appID, WorkflowName: "orders", DefinitionSnapshot: snapshot, Input: json.RawMessage(`{"secret":"never-export"}`)}
	if err := e.store.CreateWorkflowRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkWorkflowRunStatus(context.Background(), run.ID, state.WorkflowRunStatusDead, nil, nil); err != nil {
		t.Fatal(err)
	}
	rec = e.do(t, http.MethodPost, path+"/evaluate", req, nil)
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.FailureCoverage.CapturedFailures != 1 || out.FailureCoverage.MissingEligibilityEvidence != 1 || len(out.Candidates) != 1 || out.Candidates[0].Proposed.Reason != "historical_preflight_not_captured" || strings.Contains(rec.Body.String(), "never-export") {
		t.Fatalf("failure inventory HTTP %d %s", rec.Code, rec.Body.String())
	}
	if did, err := e.store.ObserveNextWorkflowFailureEligibility(context.Background(), time.Now().UTC()); err != nil || !did {
		t.Fatalf("observer %t %v", did, err)
	}
	rec = e.do(t, http.MethodPost, path+"/evaluate", req, nil)
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.Candidates[0].LaterEligibility == nil || out.Candidates[0].Proposed.Eligibility != "unknown" || out.ProposedTotals.WouldPrepare != 0 || strings.Contains(rec.Body.String(), "never-export") {
		t.Fatalf("later observation HTTP %d %s", rec.Code, rec.Body.String())
	}
	if rec = e.do(t, http.MethodPut, path, api.SetWorkflowRecoveryPolicyRequest{Policy: req.Policy}, nil); rec.Code != 403 {
		t.Fatalf("evaluation granted write %d", rec.Code)
	}
	req.ExpectedVersion = 1
	if rec = e.do(t, http.MethodPost, path+"/evaluate", req, nil); rec.Code != 409 {
		t.Fatalf("stale %d", rec.Code)
	}
	req.ExpectedVersion = 0
	req.CreatedBefore = time.Now().Add(time.Hour)
	if rec = e.do(t, http.MethodPost, path+"/evaluate", req, nil); rec.Code != 400 {
		t.Fatalf("future window %d", rec.Code)
	}
	req.CreatedBefore = time.Time{}
	req.CreatedAfter = time.Now().Add(-31 * 24 * time.Hour)
	if rec = e.do(t, http.MethodPost, path+"/evaluate", req, nil); rec.Code != 400 {
		t.Fatalf("long window %d", rec.Code)
	}
	req.CreatedAfter = time.Time{}
	req.Policy.MaxResumeCount = 0
	if rec = e.do(t, http.MethodPost, path+"/evaluate", req, nil); rec.Code != 400 {
		t.Fatalf("invalid policy %d", rec.Code)
	}
	req.Policy.MaxResumeCount = 3
	if rec = e.do(t, http.MethodPost, "/v1/apps/absent/recovery-policy/evaluate", req, nil); rec.Code != 404 {
		t.Fatalf("scope %d", rec.Code)
	}
	rec = e.do(t, http.MethodGet, path, nil, nil)
	var current api.WorkflowRecoveryPolicyResponse
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &current) != nil || current.Version != 0 {
		t.Fatalf("evaluation wrote policy %d", rec.Code)
	}
}
