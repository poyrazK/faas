// adr: 1302
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAutomationLiveBaselineAuthenticatedPromotionAndReadOnlyScopes(t *testing.T) {
	e, app, _ := consoleFixture(t)
	store := *e.Store()
	raw, _ := json.Marshal(api.WorkflowSpec{Name: "baseline", Trigger: &api.WorkflowTriggerSpec{Type: "manual"}, Steps: []api.WorkflowStepSpec{{Name: "main", Path: "/main"}}})
	draft, err := store.MutateAutomation(t.Context(), app.ID, "baseline", state.AutomationMutation{Action: "save", Draft: raw})
	if err != nil {
		t.Fatal(err)
	}
	draft, err = store.MutateAutomation(t.Context(), app.ID, draft.Name, state.AutomationMutation{Action: "publish", ExpectedVersion: draft.Version})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(api.AutomationTestSuite{Cases: []api.AutomationTestCase{{Name: "happy", Simulation: api.AutomationTestInputs{Input: json.RawMessage(`{"private":"PRIVATE-INPUT"}`)}, Expectations: []api.AutomationCheckExpectation{{Step: "main", State: "mocked"}}, Live: &api.AutomationLiveTest{Expectations: []api.AutomationLiveExpectation{{Step: "main", State: "succeeded"}}}}}})
	draft, err = store.MutateAutomation(t.Context(), app.ID, draft.Name, state.AutomationMutation{Action: "tests", ExpectedVersion: draft.Version, TestSuite: raw})
	if err != nil {
		t.Fatal(err)
	}
	root := "/v1/apps/" + app.Slug + "/automations/baseline"
	var target api.ListAutomationLiveVerificationsResponse
	if response := e.Do(t, http.MethodGet, root+"/live-verifications", nil, nil); response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &target) != nil {
		t.Fatal("missing fixture target")
	}
	start := api.StartAutomationLiveVerificationRequest{ExpectedVersion: draft.Version, ExpectedPublishedVersion: draft.PublishedVersion, ExpectedDeploymentID: target.DeploymentID, CaseName: "happy", ConfirmRealActions: true}
	var live api.AutomationLiveVerification
	response := e.Do(t, http.MethodPost, root+"/live-verifications", start, map[string]string{"Idempotency-Key": uuid.NewString()})
	if response.Code != 201 || json.Unmarshal(response.Body.Bytes(), &live) != nil {
		t.Fatal("start", response.Code, response.Body.String())
	}
	if err := store.CreateWorkflowSteps(t.Context(), live.RunID, []*state.WorkflowStep{{StepName: "main"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkWorkflowStepStatus(t.Context(), live.RunID, "main", "succeeded", 1, json.RawMessage(`"PRIVATE-OUTPUT"`), nil); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkWorkflowRunStatus(t.Context(), live.RunID, "succeeded", nil, nil); err != nil {
		t.Fatal(err)
	}
	if checked := e.Do(t, http.MethodPost, root+"/live-verifications/"+live.RunID+"/check", nil, nil); checked.Code != 200 {
		t.Fatal("check", checked.Code)
	}
	suiteID := uuid.NewString()
	save := api.SaveAutomationLiveSuiteRequest{ExpectedVersion: draft.Version, ExpectedPublishedVersion: draft.PublishedVersion, ExpectedDeploymentID: target.DeploymentID, Cases: []api.AutomationLiveSuiteMember{{Name: "happy", RunID: live.RunID}}}
	if saved := e.Do(t, http.MethodPut, root+"/live-suites/"+suiteID, save, nil); saved.Code != 200 {
		t.Fatal("save", saved.Code)
	}
	path := root + "/live-baselines/production"
	request := api.PromoteAutomationLiveBaselineRequest{SuiteID: suiteID, ExpectedVersion: draft.Version, ExpectedPublishedVersion: draft.PublishedVersion, ExpectedDeploymentID: target.DeploymentID, Policy: json.RawMessage(`{"version":2,"required_cases":["happy"],"max_candidate_age_seconds":3600}`)}
	(*e.S()).WithWorkflowRuntimeEnabled(false)
	var receipt api.AutomationLiveBaseline
	promoted := e.Do(t, http.MethodPut, path+"/promotions/"+uuid.NewString(), request, nil)
	if promoted.Code != 200 || json.Unmarshal(promoted.Body.Bytes(), &receipt) != nil || receipt.ActorAccountID != app.AccountID || receipt.Revision != 1 || receipt.CasesPassed != 1 || strings.Contains(promoted.Body.String(), "PRIVATE") {
		t.Fatalf("promotion: %d %s", promoted.Code, promoted.Body.String())
	}
	request.ExpectedRevision = 1
	for _, scope := range []string{"apps:read", "secrets:read", "deploy:write"} {
		token, hash, err := api.GenerateAPIKey()
		if err != nil {
			t.Fatal(err)
		}
		key, err := store.CreateAPIKey(t.Context(), app.AccountID, hash, "baseline-"+scope, []string{scope})
		if err != nil {
			t.Fatal(err)
		}
		wantRead, wantWrite := 200, 403
		if scope != "apps:read" {
			wantRead = 403
		}
		if scope == "deploy:write" {
			wantWrite = 200
		}
		if got := e.DoAs(t, http.MethodGet, path, nil, nil, token); got.Code != wantRead {
			t.Fatal("read scope", scope, got.Code)
		}
		if got := e.DoAs(t, http.MethodGet, root+"/live-baselines", nil, nil, token); got.Code != wantRead {
			t.Fatal("discovery scope", scope, got.Code)
		}
		got := e.DoAs(t, http.MethodPut, path+"/promotions/"+uuid.NewString(), request, nil, token)
		if got.Code != wantWrite {
			t.Fatal("write scope", scope, got.Code, got.Body.String())
		}
		if scope == "deploy:write" {
			var out api.AutomationLiveBaseline
			if json.Unmarshal(got.Body.Bytes(), &out) != nil || out.ActorAPIKeyID != key.ID || out.ActorAccountID != app.AccountID {
				t.Fatal("actor not authenticated")
			}
		}
	}
	history := e.Do(t, http.MethodGet, path+"/history", nil, nil)
	if history.Code != 200 || history.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("history", history.Code)
	}
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	(*e.H()).ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatal("unauthenticated read", w.Code)
	}
	if got := e.Do(t, http.MethodGet, strings.Replace(path, "/automations/baseline/", "/automations/foreign/", 1), nil, nil); got.Code != 404 {
		t.Fatal("foreign automation read", got.Code)
	}

	if got := e.Do(t, http.MethodGet, strings.Replace(root, "/automations/baseline", "/automations/foreign", 1)+"/live-baselines", nil, nil); got.Code != 404 {
		t.Fatal("foreign discovery", got.Code)
	}
	discoveryRequest := httptest.NewRequest(http.MethodGet, root+"/live-baselines", nil)
	discoveryResponse := httptest.NewRecorder()
	(*e.H()).ServeHTTP(discoveryResponse, discoveryRequest)
	if discoveryResponse.Code != 401 {
		t.Fatal("unauthenticated discovery", discoveryResponse.Code)
	}

	// Retirement has the same metadata-write authorization, even with runtime off.
	retirementID := uuid.NewString()
	retirementPath := path + "/retirements/" + retirementID
	retirementRequest := api.RetireAutomationLiveBaselineRequest{ExpectedRevision: 2}
	for _, raw := range []string{`{}`, `{"expected_revision":null}`, `{"expected_revision":2,"actor_account_id":"forged"}`, `{"expected_revision":2} {}`} {
		got := e.Do(t, http.MethodPut, retirementPath, json.RawMessage(raw), nil)
		if got.Code != 400 {
			t.Fatal("malformed retirement", got.Code, got.Body.String())
		}
	}
	for _, scope := range []string{"apps:read", "secrets:read", "deploy:write"} {
		token, hash, keyErr := api.GenerateAPIKey()
		if keyErr != nil {
			t.Fatal(keyErr)
		}
		key, keyErr := store.CreateAPIKey(t.Context(), app.AccountID, hash, "retire-"+scope, []string{scope})
		if keyErr != nil {
			t.Fatal(keyErr)
		}
		got := e.DoAs(t, http.MethodPut, retirementPath, retirementRequest, nil, token)
		if scope != "deploy:write" {
			if got.Code != 403 {
				t.Fatal("retirement scope", scope, got.Code)
			}
			continue
		}
		var receipt api.AutomationLiveBaselineRetirement
		if got.Code != 200 || json.Unmarshal(got.Body.Bytes(), &receipt) != nil || receipt.ActorAPIKeyID != key.ID || receipt.ActorAccountID != app.AccountID || receipt.Revision != 3 || got.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("retirement", got.Code, got.Body.String())
		}
		if retry := e.DoAs(t, http.MethodPut, retirementPath, retirementRequest, nil, token); retry.Code != 200 || retry.Body.String() != got.Body.String() {
			t.Fatal("retirement retry", retry.Code)
		}
	}
	if head := e.Do(t, http.MethodGet, path, nil, nil); head.Code != 410 || !strings.Contains(head.Body.String(), "retired") {
		t.Fatal("retired CI head", head.Code, head.Body.String())
	}
	if reuse := e.Do(t, http.MethodPut, path+"/promotions/"+uuid.NewString(), request, nil); reuse.Code != 410 {
		t.Fatal("retired name reused", reuse.Code)
	}
	retiredHistory := e.Do(t, http.MethodGet, path+"/history", nil, nil)
	var audit api.AutomationLiveBaselineHistory
	if retiredHistory.Code != 200 || json.Unmarshal(retiredHistory.Body.Bytes(), &audit) != nil || audit.Retirement == nil || len(audit.Promotions) != 2 || strings.Contains(retiredHistory.Body.String(), "PRIVATE") {
		t.Fatal("retired audit", retiredHistory.Code)
	}
	req = httptest.NewRequest(http.MethodPut, retirementPath, strings.NewReader(`{"expected_revision":2}`))
	w = httptest.NewRecorder()
	(*e.H()).ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatal("unauthenticated retirement", w.Code)
	}
	if got := e.Do(t, http.MethodPut, strings.Replace(retirementPath, "/automations/baseline/", "/automations/foreign/", 1), retirementRequest, nil); got.Code != 404 {
		t.Fatal("foreign retirement", got.Code)
	}
	runs, _, err := store.ListWorkflowRuns(t.Context(), app.ID, state.ListWorkflowRunsOpts{})
	if err != nil || len(runs) != 1 {
		t.Fatal("baseline operation admitted work", err, len(runs))
	}
}
