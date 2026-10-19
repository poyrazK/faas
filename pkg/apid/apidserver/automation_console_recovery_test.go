// adr: 1286
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func consoleRecoveryDefinition(t *testing.T, e apidserver.TestEnv, app state.App, name string) {
	t.Helper()
	spec := api.WorkflowSpec{Name: name, Trigger: &api.WorkflowTriggerSpec{Type: "manual"}, Steps: []api.WorkflowStepSpec{
		{Name: "collect", Path: "/collect"}, {Name: "deliver", Path: "/deliver", DependsOn: []string{"collect"}},
	}}
	base := "/v1/apps/" + app.Slug + "/automations/" + name
	response := e.Do(t, http.MethodPut, base, api.SaveAutomationDraftRequest{ExpectedVersion: 0, Definition: spec}, nil)
	var saved api.AutomationResponse
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &saved) != nil {
		t.Fatalf("save recovery definition: %d %s", response.Code, response.Body.String())
	}
	response = e.Do(t, http.MethodPost, base+"/publish", api.PublishAutomationRequest{ExpectedVersion: saved.Version}, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("publish recovery definition: %d %s", response.Code, response.Body.String())
	}
}

func consoleFailedRun(t *testing.T, e apidserver.TestEnv, app state.App) api.WorkflowRunResponse {
	t.Helper()
	consoleRecoveryDefinition(t, e, app, "recovery-report")
	response := e.Do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/workflows/recovery-report/runs", map[string]string{"private": "PRIVATE-RECOVERY-INPUT"}, nil)
	var run api.WorkflowRunResponse
	if response.Code != http.StatusCreated || json.Unmarshal(response.Body.Bytes(), &run) != nil {
		t.Fatalf("create recovery run: %d %s", response.Code, response.Body.String())
	}
	store := *e.Store()
	if err := store.CreateWorkflowSteps(t.Context(), run.ID, []*state.WorkflowStep{{StepName: "collect"}, {StepName: "deliver"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimNextDueWorkflowRun(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.StartWorkflowStep(t.Context(), run.ID, "collect", 1, run.Input); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkWorkflowStepAttemptStatus(t.Context(), run.ID, "collect", state.WorkflowStepStatusSucceeded, 1, nil, json.RawMessage(`"PRIVATE-RECOVERY-OUTPUT"`), nil); err != nil {
		t.Fatal(err)
	}
	consoleFailDelivery(t, e, run.ID, 0, 1)
	return run
}

func consoleFailDelivery(t *testing.T, e apidserver.TestEnv, id string, generation, attempt int) {
	t.Helper()
	ctx := state.WithWorkflowRunGeneration(t.Context(), id, generation)
	store := *e.Store()
	if _, err := store.StartWorkflowStep(ctx, id, "deliver", attempt, json.RawMessage(`{"private":"PRIVATE-RECOVERY-INPUT"}`)); err != nil {
		t.Fatal(err)
	}
	message, status := "PRIVATE-RECOVERY-ERROR", http.StatusServiceUnavailable
	if err := store.MarkWorkflowStepAttemptStatus(ctx, id, "deliver", state.WorkflowStepStatusDead, attempt, &status, nil, &message); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkWorkflowRunStatus(ctx, id, state.WorkflowRunStatusDead, nil, &message); err != nil {
		t.Fatal(err)
	}
}

func consoleControlForm(run api.WorkflowRunResponse) url.Values {
	return url.Values{"name": {run.WorkflowName}, "run_id": {run.ID}, "action_key": {uuid.NewString()}, "expected_resume_count": {"0"}}
}

func TestAutomationConsoleRecoveryPreviewResumeAndCancel(t *testing.T) {
	e, app, cookie := consoleFixture(t)
	run := consoleFailedRun(t, e, app)
	base := "/dashboard/apps/" + app.Slug + "/automations"
	path := base + "?name=" + run.WorkflowName + "&run=" + run.ID
	page := apidserver.RestartProgressDashboardGET(e, cookie, path)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Recovery preview") || !strings.Contains(page.Body.String(), "Resume failed run") {
		t.Fatalf("preview: %d %s", page.Code, page.Body.String())
	}
	current, _ := (*e.Store()).GetWorkflowRun(t.Context(), run.ID)
	if current.Status != state.WorkflowRunStatusDead || current.ResumeCount != 0 {
		t.Fatal("read-only preview changed the run")
	}
	form := consoleControlForm(run)
	denied := consolePost(e, cookie, page, base+"/resume", form)
	if denied.Code != http.StatusBadRequest || strings.Contains(denied.Body.String(), "<button class=\"primary\" type=\"submit\">Resume failed run") {
		t.Fatal("missing confirmation accepted, or failed action allowed immediate resubmission")
	}
	form.Set("confirm_resume", "yes")
	for _, invalid := range []string{"", "-1", "17", "not-a-count"} {
		form.Set("expected_resume_count", invalid)
		if response := consolePost(e, cookie, page, base+"/resume", form); response.Code != http.StatusBadRequest {
			t.Fatalf("invalid resume count accepted: %d %s", response.Code, response.Body.String())
		}
	}
	form.Set("expected_resume_count", "0")
	for n := 0; n < 2; n++ {
		accepted := consolePost(e, cookie, page, base+"/resume", form)
		if accepted.Code != http.StatusSeeOther {
			t.Fatalf("resume/replay %d: %d %s", n, accepted.Code, accepted.Body.String())
		}
	}
	current, _ = (*e.Store()).GetWorkflowRun(t.Context(), run.ID)
	history, _ := (*e.Store()).ListWorkflowResumes(t.Context(), run.ID)
	steps, _ := (*e.Store()).GetWorkflowSteps(t.Context(), run.ID)
	if current.ResumeCount != 1 || current.Status != state.WorkflowRunStatusPending || len(history) != 1 {
		t.Fatal("continuation was not admitted exactly once")
	}
	for _, step := range steps {
		if step.StepName == "collect" && (step.Status != state.WorkflowStepStatusSucceeded || string(step.Output) != `"PRIVATE-RECOVERY-OUTPUT"`) {
			t.Fatal("successful result was reopened")
		}
		if step.StepName == "deliver" && (step.Status != state.WorkflowStepStatusPending || step.RetryBase != 1) {
			t.Fatal("failed action did not retain its prior attempt and receive a fresh budget")
		}
	}
	page = apidserver.RestartProgressDashboardGET(e, cookie, path)
	if !strings.Contains(page.Body.String(), "Continuation 1") {
		t.Fatal("missing recovery audit history")
	}
	attempts := apidserver.RestartProgressDashboardGET(e, cookie, path+"&step=deliver")
	if attempts.Code != http.StatusOK || !strings.Contains(attempts.Body.String(), "Step attempts: deliver") || !strings.Contains(attempts.Body.String(), "503") {
		t.Fatalf("attempt history: %d %s", attempts.Code, attempts.Body.String())
	}
	if missing := apidserver.RestartProgressDashboardGET(e, cookie, path+"&step=not-a-step"); missing.Code != http.StatusNotFound {
		t.Fatal("unobserved step was inspected")
	}
	for _, secret := range []string{"PRIVATE-RECOVERY-INPUT", "PRIVATE-RECOVERY-OUTPUT", "PRIVATE-RECOVERY-ERROR"} {
		if strings.Contains(page.Body.String(), secret) || strings.Contains(attempts.Body.String(), secret) {
			t.Fatalf("run inspector exposed %s", secret)
		}
	}
	form = consoleControlForm(run)
	if denied := consolePost(e, cookie, page, base+"/cancel", form); denied.Code != http.StatusBadRequest {
		t.Fatal("cancellation did not require confirmation")
	}
	form.Set("confirm_cancel", "yes")
	for n := 0; n < 2; n++ {
		if response := consolePost(e, cookie, page, base+"/cancel", form); response.Code != http.StatusSeeOther {
			t.Fatalf("cancel/replay: %d %s", response.Code, response.Body.String())
		}
	}
	current, _ = (*e.Store()).GetWorkflowRun(t.Context(), run.ID)
	if current.CancelledAt == nil || current.ResumeCount != 1 {
		t.Fatal("cancellation did not preserve the continuation history")
	}
	page = apidserver.RestartProgressDashboardGET(e, cookie, path)
	if !strings.Contains(page.Body.String(), "Cancellation was requested") || strings.Contains(page.Body.String(), ">Cancel run</button>") || strings.Contains(page.Body.String(), ">Resume failed run</button>") {
		t.Fatal("cancelled run still offered an action or omitted cancellation state")
	}
}

func TestAutomationConsoleStaleRecoveryAndRuntimeGate(t *testing.T) {
	e, app, cookie := consoleFixture(t)
	run := consoleFailedRun(t, e, app)
	base := "/dashboard/apps/" + app.Slug + "/automations"
	path := base + "?name=" + run.WorkflowName + "&run=" + run.ID
	page := apidserver.RestartProgressDashboardGET(e, cookie, path)
	count := 0
	external := e.Do(t, http.MethodPost, "/v1/workflows/runs/"+run.ID+"/resume", api.ResumeWorkflowRunRequest{ExpectedResumeCount: &count}, nil)
	if external.Code != http.StatusOK {
		t.Fatal(external.Body.String())
	}
	if _, err := (*e.Store()).ClaimNextDueWorkflowRun(t.Context()); err != nil {
		t.Fatal(err)
	}
	consoleFailDelivery(t, e, run.ID, 1, 2)
	form := consoleControlForm(run)
	form.Set("confirm_resume", "yes")
	if stale := consolePost(e, cookie, page, base+"/resume", form); stale.Code != http.StatusConflict {
		t.Fatalf("stale preview: %d %s", stale.Code, stale.Body.String())
	}
	(*e.S()).WithWorkflowRuntimeEnabled(false)
	page = apidserver.RestartProgressDashboardGET(e, cookie, path)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "runtime_disabled") || strings.Contains(page.Body.String(), ">Resume failed run</button>") {
		t.Fatal("runtime-disabled preview did not fail closed")
	}
	form.Set("expected_resume_count", "1")
	form.Set("action_key", uuid.NewString())
	if blocked := consolePost(e, cookie, page, base+"/resume", form); blocked.Code != http.StatusNotImplemented {
		t.Fatalf("runtime-disabled resume: %d %s", blocked.Code, blocked.Body.String())
	}
	history, _ := (*e.Store()).ListWorkflowResumes(t.Context(), run.ID)
	if len(history) != 1 {
		t.Fatal("stale or runtime-disabled request admitted another continuation")
	}
}

func TestAutomationConsoleRunControlsEnforceIdentityAndCSRF(t *testing.T) {
	e, app, cookie := consoleFixture(t)
	run := consoleFailedRun(t, e, app)
	other, err := (*e.Store()).CreateApp(t.Context(), state.App{AccountID: app.AccountID, Slug: "recovery-other", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (*e.Store()).CreateDeployment(t.Context(), state.Deployment{AppID: other.ID, Kind: state.DeploymentKindImage,
		Status: state.DeployLive, ImageDigest: "sha256:recovery-other", Workflows: json.RawMessage("[]")}); err != nil {
		t.Fatal(err)
	}
	consoleRecoveryDefinition(t, e, other, run.WorkflowName)
	consoleRecoveryDefinition(t, e, app, "another-recovery")
	for _, target := range []struct{ slug, name string }{{other.Slug, run.WorkflowName}, {app.Slug, "another-recovery"}} {
		base := "/dashboard/apps/" + target.slug + "/automations"
		page := apidserver.RestartProgressDashboardGET(e, cookie, base+"?name="+target.name)
		if page.Code != http.StatusOK {
			t.Fatal(page.Body.String())
		}
		if inspected := apidserver.RestartProgressDashboardGET(e, cookie, base+"?name="+target.name+"&run="+run.ID); inspected.Code != http.StatusNotFound {
			t.Fatal("run from a different app or automation was inspected")
		}
		for _, action := range []string{"resume", "cancel"} {
			form := consoleControlForm(run)
			form.Set("name", target.name)
			form.Set("confirm_"+action, "yes")
			if response := consolePost(e, cookie, page, base+"/"+action, form); response.Code != http.StatusNotFound {
				t.Fatalf("cross-scope %s: %d %s", action, response.Code, response.Body.String())
			}
		}
	}
	base := "/dashboard/apps/" + app.Slug + "/automations"
	for _, action := range []string{"resume", "cancel"} {
		request := httptest.NewRequest(http.MethodPost, base+"/"+action, strings.NewReader(consoleControlForm(run).Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.AddCookie(cookie)
		response := httptest.NewRecorder()
		(*e.H()).ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatal("run action accepted missing CSRF")
		}
	}
	current, _ := (*e.Store()).GetWorkflowRun(t.Context(), run.ID)
	if current.ResumeCount != 0 || current.CancelledAt != nil {
		t.Fatal("a rejected request changed the run")
	}
}
