// adr: 1298
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAutomationConsoleSuiteComparisonReadOnlyAuthorization(t *testing.T) {
	e, app, cookie := consoleFixture(t)
	store := *e.Store()
	raw, _ := json.Marshal(api.WorkflowSpec{Name: "comparison", Trigger: &api.WorkflowTriggerSpec{Type: "manual"}, Steps: []api.WorkflowStepSpec{{Name: "main", Path: "/main"}}})
	draft, err := store.MutateAutomation(t.Context(), app.ID, "comparison", state.AutomationMutation{Action: "save", Draft: raw})
	if err != nil {
		t.Fatal(err)
	}
	draft, err = store.MutateAutomation(t.Context(), app.ID, draft.Name, state.AutomationMutation{Action: "publish", ExpectedVersion: draft.Version})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(api.AutomationTestSuite{Cases: []api.AutomationTestCase{{Name: "same-case", Simulation: api.AutomationTestInputs{Input: json.RawMessage(`{"private":"PRIVATE-INPUT"}`)}, Expectations: []api.AutomationCheckExpectation{{Step: "main", State: "mocked"}}, Live: &api.AutomationLiveTest{Expectations: []api.AutomationLiveExpectation{{Step: "main", State: "succeeded"}}}}}})
	draft, err = store.MutateAutomation(t.Context(), app.ID, draft.Name, state.AutomationMutation{Action: "tests", ExpectedVersion: draft.Version, TestSuite: raw})
	if err != nil {
		t.Fatal(err)
	}
	root := "/v1/apps/" + app.Slug + "/automations/" + draft.Name
	var target api.ListAutomationLiveVerificationsResponse
	response := e.Do(t, http.MethodGet, root+"/live-verifications", nil, nil)
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &target) != nil {
		t.Fatal("missing live target")
	}
	var suites []string
	var runs []string
	for range 2 {
		request := api.StartAutomationLiveVerificationRequest{ExpectedVersion: draft.Version, ExpectedPublishedVersion: draft.PublishedVersion, ExpectedDeploymentID: target.DeploymentID, CaseName: "same-case", ConfirmRealActions: true}
		started := e.Do(t, http.MethodPost, root+"/live-verifications", request, map[string]string{"Idempotency-Key": uuid.NewString()})
		var live api.AutomationLiveVerification
		if started.Code != 201 || json.Unmarshal(started.Body.Bytes(), &live) != nil {
			t.Fatal("start fixture", started.Code, started.Body.String())
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
		id := uuid.NewString()
		save := api.SaveAutomationLiveSuiteRequest{ExpectedVersion: draft.Version, ExpectedPublishedVersion: draft.PublishedVersion, ExpectedDeploymentID: target.DeploymentID, Cases: []api.AutomationLiveSuiteMember{{Name: "same-case", RunID: live.RunID}}}
		if got := e.Do(t, http.MethodPut, root+"/live-suites/"+id, save, nil); got.Code != 200 {
			t.Fatal("save suite", got.Code)
		}
		suites = append(suites, id)
		runs = append(runs, live.RunID)
	}
	if got := e.Do(t, http.MethodPost, root+"/live-verifications/"+runs[0]+"/check", nil, nil); got.Code != 200 {
		t.Fatal("check baseline", got.Code)
	}
	(*e.S()).WithWorkflowRuntimeEnabled(false)
	path := root + "/live-suites/" + suites[1] + "/compare/" + suites[0]
	var comparison api.AutomationLiveSuiteComparison
	response = e.Do(t, http.MethodGet, path, nil, nil)
	if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" || json.Unmarshal(response.Body.Bytes(), &comparison) != nil || comparison.Summary.Unknown != 1 || comparison.Cases[0].Candidate.Result != "unavailable" {
		t.Fatal("comparison checked an unchecked run or needed runtime", response.Code, response.Body.String())
	}
	base := "/dashboard/apps/" + app.Slug + "/automations"
	query := "?name=comparison&suite=" + suites[1] + "&baseline=" + suites[0] + "&auto=true"
	page := apidserver.RestartProgressDashboardGET(e, cookie, base+query)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "1 unknown") || !strings.Contains(page.Body.String(), "Swap baseline and candidate") || strings.Contains(page.Body.String(), `data-auto="true"`) {
		t.Fatal("dashboard did not render read-only comparison", page.Code)
	}
	for _, scopes := range [][]string{{"apps:read"}, {"secrets:read"}} {
		key, hash, err := api.GenerateAPIKey()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.CreateAPIKey(t.Context(), app.AccountID, hash, "comparison-scope", scopes); err != nil {
			t.Fatal(err)
		}
		want := 200
		if scopes[0] == "secrets:read" {
			want = 403
		}
		if got := e.DoAs(t, http.MethodGet, path, nil, nil, key); got.Code != want {
			t.Fatal("comparison scope", scopes, got.Code)
		}
	}
	for _, bad := range []string{strings.Replace(path, "/automations/comparison/", "/automations/other/", 1), root + "/live-suites/" + suites[1] + "/compare/" + uuid.NewString()} {
		if got := e.Do(t, http.MethodGet, bad, nil, nil); got.Code != 404 {
			t.Fatal("foreign comparison succeeded", got.Code)
		}
	}
	request := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	(*e.H()).ServeHTTP(w, request)
	if w.Code != 401 {
		t.Fatal("unauthenticated comparison succeeded", w.Code)
	}
	comparisonHTML := page.Body.String()[strings.Index(page.Body.String(), `<section id="automation-live-suite-comparison"`):]
	comparisonHTML = comparisonHTML[:strings.Index(comparisonHTML, "</section>")]
	for _, body := range []string{response.Body.String(), comparisonHTML} {
		if strings.Contains(body, "PRIVATE-") {
			t.Fatal("comparison exposed run payloads")
		}
	}
	retained, err := store.GetAutomationLiveVerification(t.Context(), app.ID, draft.Name, runs[1])
	if err != nil || retained.Report != nil {
		t.Fatal("comparison minted an assertion report", err)
	}
}
