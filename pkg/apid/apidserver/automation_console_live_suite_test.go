// adr: 1297
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAutomationConsoleManagedSuiteExecutionAndRecovery(t *testing.T) {
	e, app, cookie := consoleFixture(t)
	base := "/dashboard/apps/" + app.Slug + "/automations"
	page := apidserver.RestartProgressDashboardGET(e, cookie, base+"?template=scheduled-report")
	form := consoleForm(t, "browser-suite")
	var cases []api.AutomationPublishCheckScenario
	if err := json.Unmarshal([]byte(form.Get("scenarios")), &cases); err != nil {
		t.Fatal(err)
	}
	count := 2
	cases = cases[:1]
	cases[0].Live = &api.AutomationLiveTest{Expectations: []api.AutomationLiveExpectation{{Step: "collect", State: "succeeded", Output: json.RawMessage(`null`), AttemptCount: &count}, {Step: "deliver", State: "succeeded"}}}
	cases = append(cases, cases[0])
	cases[1].Name = "second"
	form.Set("scenarios", stringMustJSON(t, cases))
	for _, action := range []string{"save", "save-tests", "publish"} {
		page = consolePost(e, cookie, page, base+"/"+action, form)
		if page.Code != 200 {
			t.Fatalf("%s: %d %s", action, page.Code, page.Body.String())
		}
		draft := consoleVersion(t, e, app.Slug, "browser-suite")
		form.Set("expected_version", strconv.FormatInt(draft.Version, 10))
	}
	draft := consoleVersion(t, e, app.Slug, "browser-suite")
	root := "/v1/apps/" + app.Slug + "/automations/browser-suite"
	var target api.ListAutomationLiveVerificationsResponse
	response := e.Do(t, http.MethodGet, root+"/live-verifications", nil, nil)
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &target) != nil {
		t.Fatal("missing target")
	}
	match := regexp.MustCompile(`name="live_suite_start_id" value="([^"]+)"`).FindStringSubmatch(page.Body.String())
	if len(match) != 2 {
		t.Fatal("no fresh suite creation identity")
	}
	id := match[1]
	form.Set("live_suite_start_id", id)
	form.Set("live_published_version", strconv.FormatInt(draft.PublishedVersion, 10))
	form.Set("live_deployment_id", target.DeploymentID)
	form["live_suite_case"] = []string{cases[0].Name, cases[1].Name}
	form.Set("confirm_live_suite_start", "yes")
	sample := form.Get("sample")
	form.Set("sample", `{"input":{"unsaved":true}}`)
	blocked := consolePost(e, cookie, page, base+"/start-live-suite", form)
	if blocked.Code != 400 {
		t.Fatal("suite accepted unsaved sample input")
	}
	form.Set("sample", sample)
	page = consolePost(e, cookie, page, base+"/start-live-suite", form)
	if page.Code != 200 || !strings.Contains(page.Body.String(), `data-auto="true"`) {
		t.Fatalf("start: %d %s", page.Code, page.Body.String())
	}
	getSuite := func() api.AutomationLiveSuite {
		t.Helper()
		result := e.Do(t, http.MethodGet, root+"/live-suites/"+id, nil, nil)
		var view api.AutomationLiveSuite
		if result.Code != 200 || json.Unmarshal(result.Body.Bytes(), &view) != nil {
			t.Fatalf("suite: %d %s", result.Code, result.Body.String())
		}
		return view
	}
	first := getSuite()
	if !first.Managed || first.LinkedRuns != 1 || first.Cases[0].RunID == "" {
		t.Fatal("first admission missing")
	}
	path := base + "/live-suites/" + id + "/advance?name=browser-suite"
	if got := consolePost(e, cookie, httptest.NewRecorder(), path, url.Values{"confirm_real_actions": {"yes"}}); got.Code != 400 {
		t.Fatal("suite continuation bypassed the named CSRF envelope")
	}
	for _, auth := range []*http.Cookie{nil, apidserver.RestartProgressDashboardCookie(t, e, app.AccountID, true)} {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader("confirm_real_actions=yes"))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if auth != nil {
			request.AddCookie(auth)
		}
		w := httptest.NewRecorder()
		(*e.H()).ServeHTTP(w, request)
		if w.Code == 200 {
			t.Fatal("unauthenticated or pending MFA continuation succeeded")
		}
	}
	for _, invalid := range []url.Values{{}, {"confirm_real_actions": {"yes"}, "run_id": {"PRIVATE"}}, {"confirm_real_actions": {"no"}}} {
		if got := consolePost(e, cookie, page, path, invalid); got.Code != 400 {
			t.Fatalf("invalid continuation: %d", got.Code)
		}
	}
	consoleFinishLiveFixture(t, e, first.Cases[0].RunID)
	readPage := apidserver.RestartProgressDashboardGET(e, cookie, base+"?name=browser-suite&suite="+id+"&auto=true")
	if readPage.Code != 200 || !strings.Contains(readPage.Body.String(), `data-auto="false"`) || getSuite().Cases[0].Outcome != "unavailable" {
		t.Fatal("history read executed or checked work")
	}
	page = consolePost(e, cookie, page, path, url.Values{"confirm_real_actions": {"yes"}})
	if page.Code != 200 || len(page.Result().Cookies()) != 0 {
		t.Fatalf("advance: %d %s", page.Code, page.Body.String())
	}
	second := getSuite()
	if second.LinkedRuns != 2 || second.Cases[0].RunID != first.Cases[0].RunID || second.Cases[0].Outcome != "passed" {
		t.Fatal("unsafe sequential admission")
	}
	// Use the full page's unchanged CSRF envelope for subsequent fragments.
	consoleFinishLiveFixture(t, e, second.Cases[1].RunID)
	page = consolePost(e, cookie, readPage, path, url.Values{"confirm_real_actions": {"yes"}})
	if page.Code != 200 || getSuite().Outcome != "passed" {
		t.Fatalf("final check: %d %s", page.Code, page.Body.String())
	}
	runs, _, err := (*e.Store()).ListWorkflowRuns(t.Context(), app.ID, state.ListWorkflowRunsOpts{})
	if err != nil || len(runs) != 2 {
		t.Fatal("duplicate suite work", len(runs), err)
	}
	for _, private := range []string{"PRIVATE", "automation-sample", "automation-scenarios", "<script"} {
		if strings.Contains(page.Body.String(), private) {
			t.Fatal("unsafe progress fragment", private)
		}
	}
	if got := consolePost(e, cookie, readPage, base+"/live-suites/"+id+"/advance?name=other", url.Values{"confirm_real_actions": {"yes"}}); got.Code != 404 {
		t.Fatal("cross-automation continuation succeeded")
	}
}
