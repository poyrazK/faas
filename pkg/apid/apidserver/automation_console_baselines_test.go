// adr: 1303
package apidserver_test

import (
	"encoding/json"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestAutomationConsoleBaselinePreviewPromotionAndConflict(t *testing.T) {
	e, app, cookie := consoleFixture(t)
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

	(*e.S()).WithWorkflowRuntimeEnabled(false)
	base := "/dashboard/apps/" + app.Slug + "/automations"
	query := "?name=baseline&suite=" + suiteID + "&baseline_name=production"
	page := apidserver.RestartProgressDashboardGET(e, cookie, base+query)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "This name has no baseline yet") {
		t.Fatal("initial baseline page", page.Code)
	}
	form := url.Values{"name": {"baseline"}, "suite_id": {suiteID}, "baseline_name": {"production"}, "baseline_revision": {"0"}, "expected_version": {strconv.FormatInt(draft.Version, 10)}, "expected_published_version": {strconv.FormatInt(draft.PublishedVersion, 10)}, "expected_deployment_id": {target.DeploymentID}, "baseline_policy": {`{"version":2,"required_cases":["happy"],"max_candidate_age_seconds":3600}`}}
	// The normal console CSRF middleware protects this separate promotion form.
	request := httptest.NewRequest(http.MethodPost, base+"/promote-baseline", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(cookie)
	denied := httptest.NewRecorder()
	(*e.H()).ServeHTTP(denied, request)
	if denied.Code != 400 {
		t.Fatal("promotion accepted without csrf", denied.Code)
	}
	preview := consolePost(e, cookie, page, base+"/preview-baseline", form)
	if preview.Code != 200 || !strings.Contains(preview.Body.String(), "Qualification: passed") {
		t.Fatal("preview", preview.Code, preview.Body.String())
	}
	path := root + "/live-baselines/production"
	if got := e.Do(t, http.MethodGet, path, nil, nil); got.Code != 404 {
		t.Fatal("preview promoted a baseline", got.Code)
	}
	match := regexp.MustCompile(`name="promotion_id" value="([^"]+)"`).FindStringSubmatch(preview.Body.String())
	if len(match) != 2 {
		t.Fatal("missing stable promotion id")
	}
	form.Set("promotion_id", html.UnescapeString(match[1]))
	form.Set("confirm_baseline", "yes")
	promoted := consolePost(e, cookie, preview, base+"/promote-baseline", form)
	if promoted.Code != 200 || !strings.Contains(promoted.Body.String(), "Promotion recorded as baseline revision 1") {
		t.Fatal("promotion", promoted.Code, promoted.Body.String())
	}
	var receipt api.AutomationLiveBaseline
	got := e.Do(t, http.MethodGet, path, nil, nil)
	if json.Unmarshal(got.Body.Bytes(), &receipt) != nil || receipt.ActorAccountID != app.AccountID || receipt.ActorAPIKeyID != "" || receipt.Suite.ID != suiteID {
		t.Fatal("wrong dashboard principal", got.Body.String())
	}
	// Repeating the exact form must return the original receipt.
	retried := consolePost(e, cookie, promoted, base+"/promote-baseline", form)
	if retried.Code != 200 {
		t.Fatal("stable retry failed", retried.Code)
	}
	form.Set("promotion_id", uuid.NewString())
	conflict := consolePost(e, cookie, retried, base+"/promote-baseline", form)
	if conflict.Code != 409 {
		t.Fatal("stale revision overwrote head", conflict.Code)
	}
	refreshed := apidserver.RestartProgressDashboardGET(e, cookie, base+query)
	if refreshed.Code != 200 || !strings.Contains(refreshed.Body.String(), "Current revision 1") {
		t.Fatal("unsafe history", refreshed.Code)
	}

	discovery := e.Do(t, http.MethodGet, root+"/live-baselines", nil, nil)
	var catalog api.ListAutomationLiveBaselinesResponse
	if discovery.Code != 200 || discovery.Header().Get("Cache-Control") != "no-store" || json.Unmarshal(discovery.Body.Bytes(), &catalog) != nil || len(catalog.Baselines) != 1 || catalog.Baselines[0].Baseline.Revision != 1 || catalog.Baselines[0].EvidenceStatus != "usable" || catalog.Baselines[0].RenewalStatus != "passed" {
		t.Fatal("discovery", discovery.Code, discovery.Body.String())
	}
	if !strings.Contains(refreshed.Body.String(), "Passing reports retained") || !strings.Contains(refreshed.Body.String(), "Prepare replacement suite") {
		t.Fatal("missing dashboard catalog")
	}
	baselineSection := refreshed.Body.String()[strings.Index(refreshed.Body.String(), `<section id="automation-baselines">`):]
	baselineSection = baselineSection[:strings.Index(baselineSection, "</section>")]
	if strings.Contains(baselineSection, "PRIVATE") {
		t.Fatal("baseline panel leaked private payload")
	}
	candidateID := uuid.NewString()
	if candidate := e.Do(t, http.MethodPut, root+"/live-suites/"+candidateID, save, nil); candidate.Code != 200 {
		t.Fatal("candidate fixture", candidate.Code)
	}
	candidatePage := apidserver.RestartProgressDashboardGET(e, cookie, base+"?name=baseline&suite="+candidateID+"&baseline_name=production")
	if candidatePage.Code != 200 || !strings.Contains(candidatePage.Body.String(), "--baseline-name") || !strings.Contains(candidatePage.Body.String(), candidateID) {
		t.Fatal("missing named gate command", candidatePage.Code)
	}

	retirementMatch := regexp.MustCompile(`name="retirement_id" value="([^"]+)"`).FindStringSubmatch(candidatePage.Body.String())
	if len(retirementMatch) != 2 {
		t.Fatal("missing stable retirement id")
	}
	retirementForm := url.Values{"name": {"baseline"}, "baseline_name": {"production"}, "baseline_revision": {"1"}, "retirement_id": {html.UnescapeString(retirementMatch[1])}, "confirm_retirement": {"yes"}}
	request = httptest.NewRequest(http.MethodPost, base+"/retire-baseline", strings.NewReader(retirementForm.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(cookie)
	denied = httptest.NewRecorder()
	(*e.H()).ServeHTTP(denied, request)
	if denied.Code != 400 {
		t.Fatal("retirement accepted without csrf", denied.Code)
	}
	retirementForm.Del("confirm_retirement")
	unconfirmed := consolePost(e, cookie, candidatePage, base+"/retire-baseline", retirementForm)
	if unconfirmed.Code != 400 {
		t.Fatal("retirement accepted without confirmation", unconfirmed.Code)
	}
	retirementForm.Set("confirm_retirement", "yes")
	retirementForm.Set("baseline_revision", "2")
	stale := consolePost(e, cookie, unconfirmed, base+"/retire-baseline", retirementForm)
	if stale.Code != 409 {
		t.Fatal("stale retirement", stale.Code)
	}
	retirementForm.Set("baseline_revision", "1")
	retired := consolePost(e, cookie, stale, base+"/retire-baseline", retirementForm)
	if retired.Code != 200 || !strings.Contains(retired.Body.String(), "retired at revision 2") {
		t.Fatal("dashboard retirement", retired.Code, retired.Body.String())
	}
	if replay := consolePost(e, cookie, retired, base+"/retire-baseline", retirementForm); replay.Code != 200 {
		t.Fatal("dashboard retirement retry", replay.Code)
	}
	retiredPage := apidserver.RestartProgressDashboardGET(e, cookie, base+query)
	if retiredPage.Code != 200 || !strings.Contains(retiredPage.Body.String(), "permanently retired") || !strings.Contains(retiredPage.Body.String(), "Retired revision 2") || strings.Contains(retiredPage.Body.String(), "Preview promotion") || strings.Contains(retiredPage.Body.String(), "CI command for the selected candidate") {
		t.Fatal("unsafe retired dashboard", retiredPage.Code)
	}
	if head := e.Do(t, http.MethodGet, path, nil, nil); head.Code != 410 {
		t.Fatal("retired dashboard head", head.Code)
	}
	var active api.ListAutomationLiveBaselinesResponse
	if got := e.Do(t, http.MethodGet, root+"/live-baselines", nil, nil); got.Code != 200 || json.Unmarshal(got.Body.Bytes(), &active) != nil || len(active.Baselines) != 0 {
		t.Fatal("retired catalog", got.Code)
	}
	form.Set("promotion_id", html.UnescapeString(match[1]))
	if replay := consolePost(e, cookie, retiredPage, base+"/promote-baseline", form); replay.Code != 200 {
		t.Fatal("historical promotion retry", replay.Code)
	}
	if head := e.Do(t, http.MethodGet, path, nil, nil); head.Code != 410 {
		t.Fatal("historical retry reactivated name", head.Code)
	}
	runs, _, err := store.ListWorkflowRuns(t.Context(), app.ID, state.ListWorkflowRunsOpts{})
	if err != nil || len(runs) != 1 {
		t.Fatal("dashboard baseline flow admitted work", err, len(runs))
	}
}
