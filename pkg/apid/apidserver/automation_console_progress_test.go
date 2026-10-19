// adr: 1293
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
	"github.com/onebox-faas/faas/pkg/state"
)

func consoleProgressFixture(t *testing.T, e apidserver.TestEnv, app state.App, cookie *http.Cookie) (api.AutomationLiveVerification, *httptest.ResponseRecorder) {
	t.Helper()
	base := "/dashboard/apps/" + app.Slug + "/automations"
	page := apidserver.RestartProgressDashboardGET(e, cookie, base+"?template=scheduled-report")
	form := consoleForm(t, "progress-report")
	var cases []api.AutomationPublishCheckScenario
	if err := json.Unmarshal([]byte(form.Get("scenarios")), &cases); err != nil {
		t.Fatal(err)
	}
	attempts := 2
	cases[0].Live = &api.AutomationLiveTest{Expectations: []api.AutomationLiveExpectation{
		{Step: "collect", State: "succeeded", Output: json.RawMessage(`null`), AttemptCount: &attempts}, {Step: "deliver", State: "succeeded"},
	}}
	form.Set("scenarios", stringMustJSON(t, cases))
	for _, action := range []string{"save", "save-tests", "publish"} {
		page = consolePost(e, cookie, page, base+"/"+action, form)
		if page.Code != 200 {
			t.Fatalf("%s: %d %s", action, page.Code, page.Body.String())
		}
		current := consoleVersion(t, e, app.Slug, "progress-report")
		form.Set("expected_version", strconv.FormatInt(current.Version, 10))
	}
	var target api.ListAutomationLiveVerificationsResponse
	apiRoot := "/v1/apps/" + app.Slug + "/automations/progress-report/live-verifications"
	response := e.Do(t, http.MethodGet, apiRoot, nil, nil)
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &target) != nil {
		t.Fatal("missing live target")
	}
	draft := consoleVersion(t, e, app.Slug, "progress-report")
	response = e.Do(t, http.MethodPost, apiRoot, api.StartAutomationLiveVerificationRequest{
		ExpectedVersion: draft.Version, ExpectedPublishedVersion: draft.PublishedVersion, ExpectedDeploymentID: target.DeploymentID,
		CaseName: cases[0].Name, ConfirmRealActions: true,
	}, map[string]string{"Idempotency-Key": uuid.NewString()})
	var view api.AutomationLiveVerification
	if response.Code != 201 || json.Unmarshal(response.Body.Bytes(), &view) != nil {
		t.Fatalf("start fixture: %d %s", response.Code, response.Body.String())
	}
	page = apidserver.RestartProgressDashboardGET(e, cookie, base+"?name=progress-report&verification="+view.RunID)
	return view, page
}

func TestAutomationConsoleLiveProgressScopeCSRFAndObservation(t *testing.T) {
	e, app, cookie := consoleFixture(t)
	view, page := consoleProgressFixture(t, e, app, cookie)
	base := "/dashboard/apps/" + app.Slug + "/automations"
	path := base + "/live-verifications/" + view.RunID + "?name=progress-report"
	draft := consoleVersion(t, e, app.Slug, "progress-report")
	for _, auth := range []*http.Cookie{nil, apidserver.RestartProgressDashboardCookie(t, e, app.AccountID, true)} {
		if got := apidserver.RestartProgressDashboardGET(e, auth, path); got.Code == 200 {
			t.Fatal("unauthenticated or pending MFA progress read succeeded")
		}
	}
	other, err := (*e.Store()).CreateAccount(t.Context(), "progress-other@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if got := apidserver.RestartProgressDashboardGET(e, apidserver.RestartProgressDashboardCookie(t, e, other.ID, false), path); got.Code != 404 {
		t.Fatal("cross-account progress read succeeded")
	}
	otherApp, err := (*e.Store()).CreateApp(t.Context(), state.App{AccountID: app.AccountID, Slug: "progress-other", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	for _, wrong := range []string{
		base + "/live-verifications/" + view.RunID + "?name=other",
		"/dashboard/apps/" + otherApp.Slug + "/automations/live-verifications/" + view.RunID + "?name=progress-report",
	} {
		if got := apidserver.RestartProgressDashboardGET(e, cookie, wrong); got.Code != 404 {
			t.Fatalf("unscoped progress read: %d", got.Code)
		}
	}
	for _, query := range []string{"", "?name=" + strings.Repeat("x", api.AutomationNameMaxBytes+1)} {
		if got := apidserver.RestartProgressDashboardGET(e, cookie, base+"/live-verifications/"+view.RunID+query); got.Code != 400 {
			t.Fatal("invalid progress target accepted")
		}
	}
	get := apidserver.RestartProgressDashboardGET(e, cookie, path)
	if get.Code != 200 || get.Header().Get("Cache-Control") != "no-store" || len(get.Result().Cookies()) != 0 || !strings.Contains(get.Body.String(), `data-live-state="pending"`) {
		t.Fatal("pending progress fragment missing or CSRF cookie rotated")
	}
	if missing := consolePost(e, cookie, httptest.NewRecorder(), path, url.Values{}); missing.Code != 400 {
		t.Fatal("automatic result check bypassed CSRF")
	}
	for _, form := range []url.Values{
		{"input": {`{"override":true}`}}, {"action": {"run"}}, {"csrf_token": {"bad"}, "name": {"other"}},
	} {
		if got := consolePost(e, cookie, page, path, form); got.Code != 400 {
			t.Fatal("automatic check accepted editor data or execution intent")
		}
	}
	consoleFinishLiveFixture(t, e, view.RunID)
	(*e.S()).WithWorkflowRuntimeEnabled(false) // Observing an existing run needs no execution admission.
	get = apidserver.RestartProgressDashboardGET(e, cookie, path)
	if get.Code != 200 || !strings.Contains(get.Body.String(), `data-live-current="false"`) || strings.Contains(get.Body.String(), "Live verification passed.") {
		t.Fatal("GET evaluated assertions or issued a premature verdict")
	}
	checked := consolePost(e, cookie, page, path, url.Values{})
	if checked.Code != 200 || len(checked.Result().Cookies()) != 0 || !strings.Contains(checked.Body.String(), "Live verification passed.") || !strings.Contains(checked.Body.String(), `data-live-current="true"`) {
		t.Fatalf("automatic check: %d %s", checked.Code, checked.Body.String())
	}
	for _, unsafe := range []string{"PRIVATE", "automation-sample", "automation-scenarios", "csrf_token", "confirm_live", "<script"} {
		if strings.Contains(checked.Body.String(), unsafe) {
			t.Fatalf("result fragment contains %q", unsafe)
		}
	}
	if again := consolePost(e, cookie, page, path, url.Values{}); again.Code != 200 {
		t.Fatal("progress requests invalidated the existing form's CSRF envelope")
	}
	runs, _, err := (*e.Store()).ListWorkflowRuns(t.Context(), app.ID, state.ListWorkflowRunsOpts{})
	if err != nil || len(runs) != 1 || runs[0].ResumeCount != 0 || consoleVersion(t, e, app.Slug, "progress-report").Version != draft.Version {
		t.Fatal("observation admitted execution, resumed work or edited intent", err)
	}
}
