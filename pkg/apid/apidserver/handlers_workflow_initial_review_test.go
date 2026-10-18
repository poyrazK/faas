package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWorkflowInitialReviewHTTP(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	app := apidserver.MustSeedApp(t, e, "reviewed")
	store := *e.Store()
	ctx := context.Background()
	deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app, Kind: state.DeploymentKindImage, Status: state.DeployPending, ImageDigest: "sha256:review"})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
		t.Fatal(err)
	}
	run := &state.WorkflowRun{AppID: app, WorkflowName: "recover", Input: json.RawMessage(`{"secret":"never-export"}`), DefinitionSnapshot: json.RawMessage(`{"name":"recover","steps":[{"name":"send","run":"send","retry":{"max_attempts":2}}]}`)}
	if err := store.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateWorkflowSteps(ctx, run.ID, []*state.WorkflowStep{{StepName: "send", Input: run.Input}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.StartWorkflowStep(ctx, run.ID, "send", 1, run.Input); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkWorkflowStepAttemptStatus(ctx, run.ID, "send", state.WorkflowStepStatusDead, 1, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkWorkflowRunStatus(ctx, run.ID, state.WorkflowRunStatusDead, nil, nil); err != nil {
		t.Fatal(err)
	}
	page, err := store.ListWorkflowInitialFailures(ctx, e.Acct().ID, app, api.WorkflowInitialFailureOptions{})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("failures %+v %v", page, err)
	}
	p, err := store.SetWorkflowRecoveryPolicy(ctx, e.Acct().ID, app, api.SetWorkflowRecoveryPolicyRequest{Policy: api.WorkflowRecoveryPolicy{Enabled: true, Mode: "preview", WorkflowNames: []string{"recover"}, Triggers: []string{"initial_failure"}, FailureStatuses: []string{"dead"}, MaxResumeCount: 3, CooldownSeconds: 60, HourlyActionBudget: 10, RatePerSecond: 1, Reason: "incident"}})
	if err != nil {
		t.Fatal(err)
	}
	prepare := "/v1/apps/reviewed/workflow-failures/" + page.Items[0].ID + "/recovery-reviews"
	rec := e.Do(t, "POST", prepare, api.WorkflowInitialRecoveryReviewRequest{ExpectedPolicyVersion: p.Version}, nil)
	var review api.WorkflowInitialRecoveryReview
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &review) != nil || review.Status != "prepared" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("prepare %d %s", rec.Code, rec.Body.String())
	}
	base := "/v1/apps/reviewed/initial-recovery-reviews"
	path := base + "/" + review.ID
	if rec = e.Do(t, "GET", path, nil, nil); rec.Code != 200 {
		t.Fatalf("get %d %s", rec.Code, rec.Body.String())
	}
	if rec = e.Do(t, "GET", path+"/evidence", nil, nil); rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("evidence diagnosis %d %s", rec.Code, rec.Body.String())
	}
	repair := api.WorkflowInitialEvidenceRepairRequest{RequestID: uuid.NewString(), Reason: "verified repair"}
	if rec = e.Do(t, "POST", path+"/evidence/repair", repair, nil); rec.Code != 409 {
		t.Fatalf("unsubmitted repair %d %s", rec.Code, rec.Body.String())
	}
	if rec = e.Do(t, "POST", path+"/evidence/repair", api.WorkflowInitialEvidenceRepairRequest{RequestID: repair.RequestID}, nil); rec.Code != 400 {
		t.Fatalf("missing reason %d", rec.Code)
	}

	if rec = e.Do(t, "GET", base+"?limit=1", nil, nil); rec.Code != 200 {
		t.Fatalf("list %d %s", rec.Code, rec.Body.String())
	}
	for _, query := range []string{"?limit=101", "?limit=bad", "?limit=1&limit=2", "?after=invalid", "?unknown=1"} {
		if rec = e.Do(t, "GET", base+query, nil, nil); rec.Code != 400 {
			t.Fatalf("list query %s %d", query, rec.Code)
		}
	}
	if rec = e.Do(t, "POST", prepare, api.WorkflowInitialRecoveryReviewRequest{ExpectedPolicyVersion: p.Version + 1}, nil); rec.Code != 409 {
		t.Fatalf("version %d", rec.Code)
	}
	if rec = e.Do(t, "GET", path+"?unexpected=1", nil, nil); rec.Code != 400 {
		t.Fatalf("unknown query %d", rec.Code)
	}
	decision := api.WorkflowInitialRecoveryReviewDecisionRequest{Decision: "submit", Reason: "reviewed"}
	(*e.S()).WithWorkflowRuntimeEnabled(false)
	if rec = e.Do(t, "POST", path+"/decision", decision, nil); rec.Code != http.StatusNotImplemented {
		t.Fatalf("disabled runtime %d %s", rec.Code, rec.Body.String())
	}
	(*e.S()).WithWorkflowRuntimeEnabled(true)
	if rec = e.Do(t, "POST", path+"/decision", decision, nil); rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &review) != nil || review.Status != "submitted" || review.JobID == "" {
		t.Fatalf("submit %d %s", rec.Code, rec.Body.String())
	}
	if rec = e.Do(t, "GET", base+"/"+uuid.NewString(), nil, nil); rec.Code != 404 {
		t.Fatalf("missing review %d", rec.Code)
	}
	clear, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateAPIKey(ctx, e.Acct().ID, hash, "read-only", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	*e.Key() = clear
	if rec = e.Do(t, "GET", path+"/evidence", nil, nil); rec.Code != 200 {
		t.Fatalf("read diagnosis %d", rec.Code)
	}
	if rec = e.Do(t, "POST", path+"/evidence/repair", repair, nil); rec.Code != 403 {
		t.Fatalf("read key repair %d", rec.Code)
	}

	if rec = e.Do(t, "GET", path, nil, nil); rec.Code != 200 {
		t.Fatalf("read-only get %d", rec.Code)
	}
	if rec = e.Do(t, "POST", path+"/decision", decision, nil); rec.Code != 403 {
		t.Fatalf("read-only submission %d", rec.Code)
	}
}

func TestWorkflowInitialAutomaticPolicyHTTP(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	apidserver.MustSeedApp(t, e, "automatic-first")
	req := api.SetWorkflowRecoveryPolicyRequest{Policy: api.WorkflowRecoveryPolicy{Enabled: true, Mode: "automatic_initial", WorkflowNames: []string{"recover"}, Triggers: []string{"initial_failure"}, FailureStatuses: []string{"dead"}, MaxResumeCount: 1, CooldownSeconds: 60, HourlyActionBudget: 10, HourlyExecutionBudget: 1, PauseAfterFailures: 1, RatePerSecond: 1, Reason: "bounded recovery"}}
	path := "/v1/apps/automatic-first/recovery-policy"
	(*e.S()).WithWorkflowRuntimeEnabled(false)
	if rec := e.Do(t, "PUT", path, req, nil); rec.Code != 501 {
		t.Fatalf("disabled runtime %d %s", rec.Code, rec.Body.String())
	}
	(*e.S()).WithWorkflowRuntimeEnabled(true)
	rec := e.Do(t, "PUT", path, req, nil)
	var policy api.WorkflowRecoveryPolicyResponse
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &policy) != nil || policy.AutomaticExecution == nil {
		t.Fatalf("automatic policy %d %s", rec.Code, rec.Body.String())
	}
	if rec = e.Do(t, "GET", "/v1/apps/automatic-first/recovery-policy/queue", nil, nil); rec.Code != 200 {
		t.Fatalf("queue %d %s", rec.Code, rec.Body.String())
	}
}
