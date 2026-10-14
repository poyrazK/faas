package apidserver_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWorkflowInitialPolicyChangeHTTP(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	ctx := context.Background()
	app := apidserver.MustSeedApp(t, e, "initial-preview")
	store := *e.Store()
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app, Kind: state.DeploymentKindImage, Status: state.DeployPending, ImageDigest: "sha256:initial-preview"})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	run := &state.WorkflowRun{AppID: app, WorkflowName: "recover", Input: json.RawMessage(`{"secret":"never-export"}`), DefinitionSnapshot: json.RawMessage(`{"name":"recover","steps":[{"name":"send","run":"send","retry":{"max_attempts":2}}]}`)}
	if err = store.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err = store.CreateWorkflowSteps(ctx, run.ID, []*state.WorkflowStep{{StepName: "send", Input: run.Input}}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.StartWorkflowStep(ctx, run.ID, "send", 1, run.Input); err != nil {
		t.Fatal(err)
	}
	if err = store.MarkWorkflowStepAttemptStatus(ctx, run.ID, "send", state.WorkflowStepStatusDead, 1, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err = store.MarkWorkflowRunStatus(ctx, run.ID, state.WorkflowRunStatusDead, nil, nil); err != nil {
		t.Fatal(err)
	}
	clear, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateAPIKey(ctx, e.Acct().ID, hash, "preview-read", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	*e.Key() = clear
	req := api.SetWorkflowRecoveryPolicyRequest{Policy: api.WorkflowRecoveryPolicy{Enabled: true, Mode: "automatic_initial", WorkflowNames: []string{"recover"}, Triggers: []string{"initial_failure"}, FailureStatuses: []string{"dead"}, MaxResumeCount: 1, CooldownSeconds: 60, HourlyActionBudget: 10, HourlyExecutionBudget: 2, PauseAfterFailures: 2, RatePerSecond: 1, Reason: "proposed recovery"}}
	path := "/v1/apps/initial-preview/recovery-policy"
	for _, enabled := range []bool{true, false} {
		(*e.S()).WithWorkflowRuntimeEnabled(enabled)
		rec := e.Do(t, "POST", path+"/change-preview", req, nil)
		var out api.WorkflowRecoveryPolicyChangePreview
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.InitialFailures == nil || out.Current.Version != 0 || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("preview %d %s", rec.Code, rec.Body.String())
		}
		initial := out.InitialFailures
		if initial.RuntimeAvailable == nil || *initial.RuntimeAvailable != enabled || initial.WouldPrepare != 1 || len(initial.Candidates) != 1 {
			t.Fatalf("runtime sample %+v", initial)
		}
		if enabled && (initial.WouldSubmit != 1 || initial.Candidates[0].Execution != "would_submit") {
			t.Fatalf("enabled preview %+v", initial)
		}
		if !enabled && (initial.WouldSubmit != 0 || initial.Candidates[0].Execution != "workflow_runtime_disabled") {
			t.Fatalf("disabled preview %+v", initial)
		}
		if rec.Body.String() == "" || strings.Contains(rec.Body.String(), "never-export") {
			t.Fatal("preview disclosed workflow input")
		}
	}
	if rec := e.Do(t, "PUT", path, req, nil); rec.Code != 403 {
		t.Fatalf("read-only preview authorized mutation %d", rec.Code)
	}
	policy, err := store.GetWorkflowRecoveryPolicy(ctx, e.Acct().ID, app)
	if err != nil || policy.Version != 0 {
		t.Fatalf("preview wrote policy %+v %v", policy, err)
	}
	reviews, err := store.ListWorkflowInitialRecoveryReviews(ctx, e.Acct().ID, app, "", 100)
	if err != nil || len(reviews.Reviews) != 0 {
		t.Fatalf("preview prepared reviews %+v %v", reviews, err)
	}
	current, err := store.GetWorkflowRun(ctx, run.ID)
	if err != nil || current.ResumeCount != 0 {
		t.Fatalf("preview resumed run %+v %v", current, err)
	}
}
