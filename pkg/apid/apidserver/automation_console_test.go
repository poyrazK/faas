// adr: 1285
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/automationtemplates"
	"github.com/onebox-faas/faas/pkg/state"
)

func consoleFixture(t *testing.T) (apidserver.TestEnv, state.App, *http.Cookie) {
	t.Helper()
	e := apidserver.Setup(t, api.PlanPro)
	(*e.S()).WithWorkflowRuntimeEnabled(true)
	app, err := (*e.Store()).CreateApp(t.Context(), state.App{AccountID: (*e.Acct()).ID, Slug: "automation-console", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	_, err = (*e.Store()).CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive, ImageDigest: "sha256:console", Workflows: json.RawMessage("[]")})
	if err != nil {
		t.Fatal(err)
	}
	return e, app, apidserver.RestartProgressDashboardCookie(t, e, app.AccountID, false)
}
func consoleForm(t *testing.T, name string) url.Values {
	t.Helper()
	bundle, err := automationtemplates.Load("scheduled-report")
	if err != nil {
		t.Fatal(err)
	}
	bundle.Definition.Name = name
	definition, _ := json.Marshal(bundle.Definition)
	sample, _ := json.Marshal(bundle.Sample)
	scenarios, _ := json.Marshal(bundle.Scenarios)
	return url.Values{"name": {name}, "definition": {string(definition)}, "sample": {string(sample)}, "scenarios": {string(scenarios)}, "expected_version": {"0"}, "action_key": {uuid.NewString()}}
}
func consolePost(e apidserver.TestEnv, session *http.Cookie, page *httptest.ResponseRecorder, path string, form url.Values) *httptest.ResponseRecorder {
	form.Set("csrf_token", apidserver.DashboardInputToken(page.Body.String()))
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(session)
	if cookie := apidserver.FindDashboardCookie(page.Result().Cookies(), "gregale_automation_csrf"); cookie != nil {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	(*e.H()).ServeHTTP(response, request)
	return response
}
func consoleVersion(t *testing.T, e apidserver.TestEnv, slug, name string) api.AutomationResponse {
	t.Helper()
	response := e.Do(t, http.MethodGet, "/v1/apps/"+slug+"/automations/"+name, nil, nil)
	var draft api.AutomationResponse
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &draft) != nil {
		t.Fatalf("draft: %d %s", response.Code, response.Body.String())
	}
	return draft
}

func TestAutomationConsoleFirstRunJourney(t *testing.T) {
	e, app, cookie := consoleFixture(t)
	base := "/dashboard/apps/" + app.Slug + "/automations"
	page := apidserver.RestartProgressDashboardGET(e, cookie, base+"?template=scheduled-report")
	if page.Code != 200 || !strings.Contains(page.Body.String(), "Try sample data") {
		t.Fatalf("starter: %d %s", page.Code, page.Body.String())
	}
	form := consoleForm(t, "my-first-report")
	for _, action := range []string{"validate", "simulate"} {
		page = consolePost(e, cookie, page, base+"/"+action, form)
		if page.Code != 200 {
			t.Fatalf("%s: %d %s", action, page.Code, page.Body.String())
		}
	}
	rows, _ := (*e.Store()).ListAutomations(t.Context(), app.ID)
	runs, _, _ := (*e.Store()).ListWorkflowRuns(t.Context(), app.ID, state.ListWorkflowRunsOpts{})
	if len(rows) != 0 || len(runs) != 0 {
		t.Fatal("GET, validation or simulation persisted a draft or run")
	}
	page = consolePost(e, cookie, page, base+"/save", form)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "my-first-report") || !strings.Contains(page.Body.String(), "mock_outputs") {
		t.Fatalf("save lost authoring state: %d %s", page.Code, page.Body.String())
	}
	draft := consoleVersion(t, e, app.Slug, "my-first-report")
	form.Set("expected_version", strconv.FormatInt(draft.Version, 10))
	policy := e.Do(t, http.MethodPut, "/v1/apps/"+app.Slug+"/automations:publish-policy", api.SetAutomationPublishPolicyRequest{Mode: "scenarios"}, nil)
	if policy.Code != 200 {
		t.Fatalf("policy: %d %s", policy.Code, policy.Body.String())
	}
	page = consolePost(e, cookie, page, base+"/publish", form)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "Checks passed and the draft was published") {
		t.Fatalf("publish: %d %s", page.Code, page.Body.String())
	}
	published := consoleVersion(t, e, app.Slug, "my-first-report")
	if published.Published == nil || published.Enabled {
		t.Fatal("publication missing or disabled starter schedule was enabled")
	}
	revisions, _, err := (*e.Store()).ListAutomationRevisions(t.Context(), app.ID, published.Name, state.AutomationRevisionListOptions{Limit: 10})
	if err != nil || len(revisions) != 1 || revisions[0].CheckEvidence == nil {
		t.Fatalf("missing retained checks: %+v %v", revisions, err)
	}
	form.Set("expected_version", strconv.FormatInt(published.Version, 10))
	denied := consolePost(e, cookie, page, base+"/run", form)
	if denied.Code != 400 {
		t.Fatal("run did not require real-action confirmation")
	}
	form.Set("confirm_run", "yes")
	form.Set("action_key", uuid.NewString())
	accepted := consolePost(e, cookie, denied, base+"/run", form)
	if accepted.Code != 303 {
		t.Fatalf("run: %d %s", accepted.Code, accepted.Body.String())
	}
	replay := consolePost(e, cookie, denied, base+"/run", form)
	if replay.Code != 303 || replay.Header().Get("Location") != accepted.Header().Get("Location") {
		t.Fatal("double submit created a different run")
	}
	detail := apidserver.RestartProgressDashboardGET(e, cookie, accepted.Header().Get("Location"))
	if detail.Code != 200 || !strings.Contains(detail.Body.String(), "Selected run") || !strings.Contains(detail.Body.String(), "pending") {
		t.Fatalf("inspector: %d %s", detail.Code, detail.Body.String())
	}
	runs, _, _ = (*e.Store()).ListWorkflowRuns(t.Context(), app.ID, state.ListWorkflowRunsOpts{})
	if len(runs) != 1 {
		t.Fatalf("expected exactly one admitted run, got %d", len(runs))
	}
}

func TestAutomationConsoleAuthorizationAndReadOnlySetup(t *testing.T) {
	e, app, cookie := consoleFixture(t)
	base := "/dashboard/apps/" + app.Slug + "/automations"
	for _, auth := range []*http.Cookie{nil, apidserver.RestartProgressDashboardCookie(t, e, app.AccountID, true)} {
		if page := apidserver.RestartProgressDashboardGET(e, auth, base); page.Code == 200 {
			t.Fatal("anonymous or pending MFA access accepted")
		}
	}
	other, err := (*e.Store()).CreateAccount(t.Context(), "other-console@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if page := apidserver.RestartProgressDashboardGET(e, apidserver.RestartProgressDashboardCookie(t, e, other.ID, false), base); page.Code != 404 {
		t.Fatal("cross-account app read accepted")
	}
	page := apidserver.RestartProgressDashboardGET(e, cookie, base+"?template=scheduled-report")
	form := consoleForm(t, "private-report")
	missing := consolePost(e, cookie, httptest.NewRecorder(), base+"/save", form)
	if missing.Code != 400 {
		t.Fatal("missing CSRF accepted")
	}
	otherApp, _ := (*e.Store()).CreateApp(t.Context(), state.App{AccountID: app.AccountID, Slug: "another-console", Type: state.AppTypeApp})
	crossApp := consolePost(e, cookie, page, "/dashboard/apps/"+otherApp.Slug+"/automations/save", form)
	if crossApp.Code != 400 {
		t.Fatal("CSRF envelope was not app-bound")
	}
	unknown := consolePost(e, cookie, page, base+"/delete-all", form)
	if unknown.Code != 404 {
		t.Fatal("arbitrary mutation accepted")
	}
	for _, query := range []string{"?template=../../secrets", "?run=" + uuid.NewString()} {
		if response := apidserver.RestartProgressDashboardGET(e, cookie, base+query); response.Code != 404 {
			t.Fatal("unknown template or unscoped run accepted")
		}
	}
	rows, _ := (*e.Store()).ListAutomations(t.Context(), app.ID)
	if len(rows) != 0 {
		t.Fatal("rejected requests saved intent")
	}
}

func TestAutomationConsoleSessionActorAndEmailGate(t *testing.T) {
	e, app, owner := consoleFixture(t)
	unverified, err := (*e.Store()).CreateAccountWithPersonalOrg(t.Context(), state.CreateAccountWithPersonalOrgParams{
		Email: "unverified-console@example.com", Plan: api.PlanPro, RequireEmailVerification: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	unverifiedApp, err := (*e.Store()).CreateApp(t.Context(), state.App{AccountID: unverified.Account.ID, Slug: "unverified-console", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		app    state.App
		cookie *http.Cookie
		status int
		rows   int
	}{
		{"verified session ignores competing bearer", app, owner, http.StatusOK, 1},
		{"unverified owner can read but cannot save", unverifiedApp, apidserver.RestartProgressDashboardCookie(t, e, unverified.Account.ID, false), http.StatusForbidden, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			base := "/dashboard/apps/" + test.app.Slug + "/automations"
			page := apidserver.RestartProgressDashboardGET(e, test.cookie, base+"?template=scheduled-report")
			if page.Code != http.StatusOK {
				t.Fatalf("read: %d %s", page.Code, page.Body.String())
			}
			form := consoleForm(t, "session-report")
			form.Set("csrf_token", apidserver.DashboardInputToken(page.Body.String()))
			request := httptest.NewRequest(http.MethodPost, base+"/save", strings.NewReader(form.Encode()))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			request.Header.Set("Authorization", "Bearer invalid-competing-credential")
			request.AddCookie(test.cookie)
			request.AddCookie(apidserver.FindDashboardCookie(page.Result().Cookies(), "gregale_automation_csrf"))
			response := httptest.NewRecorder()
			(*e.H()).ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("save: %d %s", response.Code, response.Body.String())
			}
			rows, err := (*e.Store()).ListAutomations(t.Context(), test.app.ID)
			if err != nil || len(rows) != test.rows {
				t.Fatalf("saved drafts = %d, want %d; error = %v", len(rows), test.rows, err)
			}
		})
	}
}

func TestAutomationConsoleStaleAndUnsavedDraftsCannotPublish(t *testing.T) {
	e, app, cookie := consoleFixture(t)
	base := "/dashboard/apps/" + app.Slug + "/automations"
	page := apidserver.RestartProgressDashboardGET(e, cookie, base+"?template=scheduled-report")
	form := consoleForm(t, "fenced-report")
	saved := consolePost(e, cookie, page, base+"/save", form)
	draft := consoleVersion(t, e, app.Slug, "fenced-report")
	stale := consolePost(e, cookie, saved, base+"/save", form)
	if stale.Code != 409 {
		t.Fatal("stale draft overwrote current intent")
	}
	form.Set("expected_version", strconv.FormatInt(draft.Version, 10))
	form.Set("definition", strings.Replace(form.Get("definition"), "/collect-report", "/changed-report", 1))
	// Modify an actual field regardless of the starter's route spelling.
	var spec api.WorkflowSpec
	if err := json.Unmarshal([]byte(form.Get("definition")), &spec); err != nil {
		t.Fatal(err)
	}
	spec.Steps[0].Path = "/changed-report"
	raw, _ := json.Marshal(spec)
	form.Set("definition", string(raw))
	denied := consolePost(e, cookie, stale, base+"/publish", form)
	if denied.Code != 409 {
		t.Fatalf("unsaved changes published: %d %s", denied.Code, denied.Body.String())
	}
	if current := consoleVersion(t, e, app.Slug, "fenced-report"); current.Published != nil {
		t.Fatal("unsaved draft was published")
	}
}

func TestAutomationConsoleFailedChecksAndRuntimeGate(t *testing.T) {
	e, app, cookie := consoleFixture(t)
	base := "/dashboard/apps/" + app.Slug + "/automations"
	page := apidserver.RestartProgressDashboardGET(e, cookie, base+"?template=scheduled-report")
	form := consoleForm(t, "checked-report")
	page = consolePost(e, cookie, page, base+"/save", form)
	draft := consoleVersion(t, e, app.Slug, "checked-report")
	form.Set("expected_version", strconv.FormatInt(draft.Version, 10))
	var scenarios []api.AutomationPublishCheckScenario
	if err := json.Unmarshal([]byte(form.Get("scenarios")), &scenarios); err != nil {
		t.Fatal(err)
	}
	scenarios[0].Expectations[0].State = "failed"
	raw, _ := json.Marshal(scenarios)
	original := form.Get("scenarios")
	form.Set("scenarios", string(raw))
	denied := consolePost(e, cookie, page, base+"/publish", form)
	if denied.Code < 400 {
		t.Fatal("failed scenario published a draft")
	}
	if current := consoleVersion(t, e, app.Slug, "checked-report"); current.Published != nil {
		t.Fatal("failed check left a publication")
	}
	form.Set("scenarios", original)
	page = consolePost(e, cookie, denied, base+"/publish", form)
	if page.Code != 200 {
		t.Fatalf("valid publication: %d %s", page.Code, page.Body.String())
	}
	(*e.S()).WithWorkflowRuntimeEnabled(false)
	form.Set("confirm_run", "yes")
	blocked := consolePost(e, cookie, page, base+"/run", form)
	if blocked.Code != 501 {
		t.Fatalf("runtime disabled admission = %d %s", blocked.Code, blocked.Body.String())
	}
	runs, _, _ := (*e.Store()).ListWorkflowRuns(t.Context(), app.ID, state.ListWorkflowRunsOpts{})
	if len(runs) != 0 {
		t.Fatal("disabled runtime admitted work")
	}
}

func TestAutomationConsoleDoesNotWeakenCoveragePolicy(t *testing.T) {
	e, app, cookie := consoleFixture(t)
	base := "/dashboard/apps/" + app.Slug + "/automations"
	page := apidserver.RestartProgressDashboardGET(e, cookie, base+"?template=scheduled-report")
	form := consoleForm(t, "coverage-report")
	var spec api.WorkflowSpec
	if err := json.Unmarshal([]byte(form.Get("definition")), &spec); err != nil {
		t.Fatal(err)
	}
	spec.Steps[0].When = &api.WorkflowGuardSpec{Ref: "input.report", Op: "eq", Value: json.RawMessage(`"daily-summary"`)}
	raw, _ := json.Marshal(spec)
	form.Set("definition", string(raw))
	page = consolePost(e, cookie, page, base+"/save", form)
	draft := consoleVersion(t, e, app.Slug, "coverage-report")
	form.Set("expected_version", strconv.FormatInt(draft.Version, 10))
	policy := e.Do(t, http.MethodPut, "/v1/apps/"+app.Slug+"/automations:publish-policy", api.SetAutomationPublishPolicyRequest{Mode: "coverage"}, nil)
	if policy.Code != 200 {
		t.Fatal(policy.Body.String())
	}
	denied := consolePost(e, cookie, page, base+"/publish", form)
	if denied.Code < 400 {
		t.Fatal("a success-only scenario bypassed required false-guard coverage")
	}
	current := consoleVersion(t, e, app.Slug, "coverage-report")
	if current.Published != nil {
		t.Fatal("coverage failure still published")
	}
}

func TestAutomationConsoleInspectorHidesCrossAppRunsAndPayloads(t *testing.T) {
	e, app, cookie := consoleFixture(t)
	base := "/dashboard/apps/" + app.Slug + "/automations"
	form := consoleForm(t, "private-report")
	page := apidserver.RestartProgressDashboardGET(e, cookie, base+"?template=scheduled-report")
	page = consolePost(e, cookie, page, base+"/save", form)
	draft := consoleVersion(t, e, app.Slug, "private-report")
	form.Set("expected_version", strconv.FormatInt(draft.Version, 10))
	page = consolePost(e, cookie, page, base+"/publish", form)
	if page.Code != 200 {
		t.Fatal(page.Body.String())
	}
	input := json.RawMessage(`{"report":"PRIVATE-RUN-PAYLOAD"}`)
	run := e.Do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/workflows/private-report/runs", input, nil)
	var admitted api.WorkflowRunResponse
	if run.Code != 201 || json.Unmarshal(run.Body.Bytes(), &admitted) != nil {
		t.Fatalf("run: %d %s", run.Code, run.Body.String())
	}
	query := "?name=private-report&run=" + admitted.ID
	view := apidserver.RestartProgressDashboardGET(e, cookie, base+query)
	if view.Code != 200 || strings.Contains(view.Body.String(), "PRIVATE-RUN-PAYLOAD") {
		t.Fatal("inspector exposed a real run input")
	}
	other, err := (*e.Store()).CreateApp(t.Context(), state.App{AccountID: app.AccountID, Slug: "private-other", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	otherPage := apidserver.RestartProgressDashboardGET(e, cookie, "/dashboard/apps/"+other.Slug+"/automations"+query)
	if otherPage.Code != 404 || strings.Contains(otherPage.Body.String(), admitted.ID) {
		t.Fatal("cross-app run inspection accepted")
	}
}
