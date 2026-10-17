// adr: 1291
package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAutomationConsoleLiveAPIFencesAndSafeReports(t *testing.T) {
	e, app, cookie := consoleFixture(t)
	base := "/dashboard/apps/" + app.Slug + "/automations"
	page := apidserver.RestartProgressDashboardGET(e, cookie, base+"?template=scheduled-report")
	form := consoleForm(t, "live-http")
	var cases []api.AutomationPublishCheckScenario
	if err := json.Unmarshal([]byte(form.Get("scenarios")), &cases); err != nil {
		t.Fatal(err)
	}
	count := 2
	cases[0].Live = &api.AutomationLiveTest{Expectations: []api.AutomationLiveExpectation{{Step: "collect", State: "succeeded", Output: json.RawMessage(`null`), AttemptCount: &count}, {Step: "deliver", State: "succeeded"}}}
	form.Set("scenarios", stringMustJSON(t, cases))
	for _, action := range []string{"save", "save-tests", "publish"} {
		page = consolePost(e, cookie, page, base+"/"+action, form)
		if page.Code != 200 {
			t.Fatalf("%s: %d %s", action, page.Code, page.Body.String())
		}
		current := consoleVersion(t, e, app.Slug, "live-http")
		form.Set("expected_version", strconv.FormatInt(current.Version, 10))
	}
	draft := consoleVersion(t, e, app.Slug, "live-http")
	path := "/v1/apps/" + app.Slug + "/automations/" + draft.Name + "/live-verifications"
	listing := e.Do(t, http.MethodGet, path, nil, nil)
	var target api.ListAutomationLiveVerificationsResponse
	if listing.Code != 200 || json.Unmarshal(listing.Body.Bytes(), &target) != nil || target.DeploymentID == "" {
		t.Fatal("verification target unavailable")
	}
	body := api.StartAutomationLiveVerificationRequest{ExpectedVersion: draft.Version, ExpectedPublishedVersion: draft.PublishedVersion, ExpectedDeploymentID: target.DeploymentID, CaseName: cases[0].Name, ConfirmRealActions: true}
	headers := map[string]string{"Idempotency-Key": uuid.NewString()}
	if got := e.Do(t, http.MethodPost, path, body, nil); got.Code != 400 {
		t.Fatalf("missing key: %d", got.Code)
	}
	unconfirmed := body
	unconfirmed.ConfirmRealActions = false
	if got := e.Do(t, http.MethodPost, path, unconfirmed, headers); got.Code != 400 {
		t.Fatalf("missing confirmation: %d", got.Code)
	}
	unknown := map[string]any{}
	_ = json.Unmarshal([]byte(stringMustJSON(t, body)), &unknown)
	unknown["input"] = map[string]any{"override": true}
	if got := e.Do(t, http.MethodPost, path, unknown, headers); got.Code != 400 {
		t.Fatalf("input override: %d", got.Code)
	}
	oversized := json.RawMessage(`{"case_name":"` + strings.Repeat("x", int(api.AutomationLiveStartMaxBytes)) + `"}`)
	if got := e.Do(t, http.MethodPost, path, oversized, headers); got.Code != 413 {
		t.Fatalf("oversized request: %d", got.Code)
	}
	first := e.Do(t, http.MethodPost, path, body, headers)
	var view api.AutomationLiveVerification
	if first.Code != 201 || json.Unmarshal(first.Body.Bytes(), &view) != nil || first.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("start: %d %s", first.Code, first.Body.String())
	}
	(*e.S()).WithWorkflowRuntimeEnabled(false)
	replayed := e.Do(t, http.MethodPost, path, body, headers)
	var again api.AutomationLiveVerification
	if replayed.Code != 201 || replayed.Header().Get("Idempotent-Replayed") != "true" || json.Unmarshal(replayed.Body.Bytes(), &again) != nil || again.RunID != view.RunID {
		t.Fatal("retained replay failed during runtime outage")
	}
	(*e.S()).WithWorkflowRuntimeEnabled(true)
	changed := body
	changed.ExpectedDeploymentID = uuid.NewString()
	if got := e.Do(t, http.MethodPost, path, changed, headers); got.Code != 409 {
		t.Fatalf("changed replay: %d", got.Code)
	}
	if got := e.Do(t, http.MethodPost, path, changed, map[string]string{"Idempotency-Key": uuid.NewString()}); got.Code != 409 {
		t.Fatalf("stale deployment: %d", got.Code)
	}
	checkPath := path + "/" + view.RunID + "/check"
	pending := e.Do(t, http.MethodPost, checkPath, nil, nil)
	if pending.Code != 200 || json.Unmarshal(pending.Body.Bytes(), &again) != nil || again.Report != nil {
		t.Fatal("pending run received a verdict")
	}
	if got := e.Do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/automations/other/live-verifications/"+view.RunID, nil, nil); got.Code != 404 {
		t.Fatal("cross-automation verification read succeeded")
	}
	consoleFinishLiveFixture(t, e, view.RunID)
	checked := e.Do(t, http.MethodPost, checkPath, nil, nil)
	if checked.Code != 200 || json.Unmarshal(checked.Body.Bytes(), &again) != nil || again.Report == nil || !again.Report.Passed || !again.ReportCurrent {
		t.Fatalf("check: %d %s", checked.Code, checked.Body.String())
	}
	for _, secret := range []string{"PRIVATE", `"input":`, `"output":`, "expectations", "mock_outputs"} {
		if strings.Contains(checked.Body.String(), secret) {
			t.Fatalf("safe result exposed %q", secret)
		}
	}
	form.Set("live_published_version", strconv.FormatInt(draft.PublishedVersion, 10))
	form.Set("live_deployment_id", target.DeploymentID)
	form.Set("live_case", body.CaseName)
	form.Set("confirm_live", "yes")
	if missing := consolePost(e, cookie, httptest.NewRecorder(), base+"/start-live", form); missing.Code != 400 {
		t.Fatal("live start bypassed dashboard CSRF")
	}
	form.Set("sample", `{"input":{"unsaved":true}}`)
	if got := consolePost(e, cookie, page, base+"/start-live", form); got.Code != 400 || !strings.Contains(got.Body.String(), "Save your current sample") {
		t.Fatal("live start accepted unsaved test data")
	}
	runs, _, err := (*e.Store()).ListWorkflowRuns(t.Context(), app.ID, state.ListWorkflowRunsOpts{})
	if err != nil || len(runs) != 1 {
		t.Fatal("replays or checks created additional real runs", err)
	}
}

// Complete only the fixture's durable state. No guest or customer handler is
// executed by portable HTTP/browser qualification.
func consoleFinishLiveFixture(t *testing.T, e apidserver.TestEnv, runID string) {
	t.Helper()
	consoleFinishLiveFixtureContext(t.Context(), t, e, runID)
}

func consoleFinishLiveFixtureContext(ctx context.Context, t *testing.T, e apidserver.TestEnv, runID string) {
	t.Helper()
	store := *e.Store()
	if err := store.CreateWorkflowSteps(ctx, runID, []*state.WorkflowStep{{StepName: "collect"}, {StepName: "deliver"}}); err != nil {
		t.Error(err)
		return
	}
	if err := store.MarkWorkflowStepStatus(ctx, runID, "collect", state.WorkflowStepStatusSucceeded, 2, json.RawMessage(`null`), nil); err != nil {
		t.Error(err)
		return
	}
	if err := store.MarkWorkflowStepStatus(ctx, runID, "deliver", state.WorkflowStepStatusSucceeded, 1, json.RawMessage(`"PRIVATE-DELIVERY-OUTPUT"`), nil); err != nil {
		t.Error(err)
		return
	}
	if err := store.MarkWorkflowRunStatus(ctx, runID, state.WorkflowRunStatusSucceeded, json.RawMessage(`"PRIVATE-RUN-OUTPUT"`), nil); err != nil {
		t.Error(err)
	}
}
