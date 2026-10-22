package apidserver_test

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
	"time"
)

func TestWorkflowRetryEvidenceHTTP(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	ctx := context.Background()
	store := *e.Store()
	app := apidserver.MustSeedApp(t, e, "retry-http")
	deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc", Status: state.DeployPending})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
		t.Fatal(err)
	}
	run := &state.WorkflowRun{AppID: app, WorkflowName: "recover", DefinitionSnapshot: json.RawMessage(`{"name":"recover","steps":[{"name":"send","run":"send","retry":{"max_attempts":2}}]}`)}
	if err = store.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err = store.MarkWorkflowRunStatus(ctx, run.ID, state.WorkflowRunStatusDead, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err = store.CreateWorkflowSteps(ctx, run.ID, []*state.WorkflowStep{{StepName: "send", Status: state.WorkflowStepStatusDead, Attempt: 1}}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateEventRecovery(ctx, e.Acct().ID, app, api.EventRecoveryRequest{RequestID: uuid.NewString(), WorkflowRuns: &api.WorkflowRunRecoveryPlan{Name: "recover", Runs: []api.WorkflowRunRecoveryTarget{{RunID: run.ID}}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ProcessNextEventRecovery(ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ClaimNextDueWorkflowRun(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = store.StartWorkflowStep(ctx, run.ID, "send", 2, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	message := "provider unavailable"
	code := 503
	if err = store.MarkWorkflowStepAttemptStatus(ctx, run.ID, "send", state.WorkflowStepStatusDead, 2, &code, nil, &message); err != nil {
		t.Fatal(err)
	}
	if err = store.MarkWorkflowRunStatus(ctx, run.ID, state.WorkflowRunStatusDead, nil, &message); err != nil {
		t.Fatal(err)
	}
	policy := api.WorkflowRecoveryPolicy{Enabled: true, Mode: "automatic_retry", WorkflowNames: []string{"recover"}, Triggers: []string{"saved_failure"}, FailureStatuses: []string{"dead"}, MaxResumeCount: 3, CooldownSeconds: 60, HourlyActionBudget: 10, HourlyExecutionBudget: 2, PauseAfterFailures: 2, RatePerSecond: 1, Reason: "provider incident"}
	if _, err = store.SetWorkflowRecoveryPolicy(ctx, e.Acct().ID, app, api.SetWorkflowRecoveryPolicyRequest{Policy: policy}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ProcessNextWorkflowRecoveryPolicy(ctx, time.Now().UTC().Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	reviews, err := store.ListWorkflowRecoveryPolicyReviews(ctx, e.Acct().ID, app, "", 100)
	if err != nil || len(reviews.Reviews) != 1 || reviews.Reviews[0].AutomaticExecution == nil {
		t.Fatalf("reviews %+v %v", reviews, err)
	}
	review := reviews.Reviews[0]
	id := review.AutomaticExecution.Actions[0].ActionID

	path := "/v1/apps/retry-http/recovery-policy/reviews/" + review.ID + "/actions/" + id + "/evidence"
	rec := e.Do(t, "GET", path, nil, nil)
	var d api.WorkflowRetryEvidenceDiagnosis
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &d) != nil || d.Reason != "retry_not_applied" || d.ResumeCount != 2 || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("diagnosis %d %s", rec.Code, rec.Body.String())
	}

	parentPath := path + "/parent"
	if rec = e.Do(t, "GET", parentPath, nil, nil); rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &d) != nil || d.EvidenceKind != "parent" || d.ResumeCount != 1 || d.Reason != "already_recorded" || d.Repairable {
		t.Fatalf("parent diagnosis %d %s", rec.Code, rec.Body.String())
	}
	req := api.WorkflowInitialEvidenceRepairRequest{RequestID: uuid.NewString(), Reason: "repair verified outcome"}
	if rec = e.Do(t, "POST", parentPath+"/repair", req, nil); rec.Code != 409 {
		t.Fatalf("already recorded parent repair %d", rec.Code)
	}
	if rec = e.Do(t, "POST", path+"/repair", req, nil); rec.Code != 409 {
		t.Fatalf("unapplied repair %d %s", rec.Code, rec.Body.String())
	}
	if rec = e.Do(t, "POST", path+"/repair", api.WorkflowInitialEvidenceRepairRequest{RequestID: req.RequestID}, nil); rec.Code != 400 {
		t.Fatalf("invalid request %d", rec.Code)
	}
	if rec = e.Do(t, "GET", path+"?unexpected=1", nil, nil); rec.Code != 400 {
		t.Fatalf("query validation %d", rec.Code)
	}
	if rec = e.Do(t, "GET", "/v1/apps/retry-http/recovery-policy/reviews/"+review.ID+"/actions/"+uuid.NewString()+"/evidence", nil, nil); rec.Code != 404 {
		t.Fatalf("action scope %d", rec.Code)
	}
	clear, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateAPIKey(ctx, e.Acct().ID, hash, "read-evidence", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	*e.Key() = clear
	if rec = e.Do(t, "GET", parentPath, nil, nil); rec.Code != 200 {
		t.Fatalf("read parent diagnosis %d", rec.Code)
	}
	if rec = e.Do(t, "POST", parentPath+"/repair", req, nil); rec.Code != 403 {
		t.Fatalf("read parent repair %d", rec.Code)
	}

	if rec = e.Do(t, "GET", path, nil, nil); rec.Code != 200 {
		t.Fatalf("read diagnosis %d", rec.Code)
	}
	if rec = e.Do(t, "POST", path+"/repair", req, nil); rec.Code != 403 {
		t.Fatalf("read key repair %d", rec.Code)
	}
}
