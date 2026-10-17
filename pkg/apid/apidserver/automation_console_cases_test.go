// adr: 1289
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAutomationConsoleSavedCasesJourney(t *testing.T) {
	e, app, cookie := consoleFixture(t)
	base := "/dashboard/apps/" + app.Slug + "/automations"
	page := apidserver.RestartProgressDashboardGET(e, cookie, base+"?template=scheduled-report")
	form := consoleForm(t, "retained-report")
	page = consolePost(e, cookie, page, base+"/save", form)
	if page.Code != 200 {
		t.Fatalf("save: %d", page.Code)
	}
	draft := consoleVersion(t, e, app.Slug, "retained-report")
	form.Set("expected_version", strconv.FormatInt(draft.Version, 10))
	page = consolePost(e, cookie, page, base+"/save-tests", form)
	if page.Code != 200 {
		t.Fatalf("save tests: %d %s", page.Code, page.Body.String())
	}
	saved := consoleVersion(t, e, app.Slug, draft.Name)
	if saved.TestSuite == nil || len(saved.TestSuite.Cases) == 0 {
		t.Fatal("saved suite missing")
	}
	form.Set("expected_version", strconv.FormatInt(saved.Version, 10))
	page = apidserver.RestartProgressDashboardGET(e, cookie, base+"?name="+draft.Name)
	if !strings.Contains(page.Body.String(), saved.TestSuite.Cases[0].Name) || !strings.Contains(page.Body.String(), "mock_outputs") {
		t.Fatal("reopen lost custom-named test data")
	}
	page = consolePost(e, cookie, page, base+"/run-tests", form)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "All saved cases passed.") || !strings.Contains(page.Body.String(), "Results are current") {
		t.Fatalf("run: %d %s", page.Code, page.Body.String())
	}
	report := consoleVersion(t, e, app.Slug, draft.Name)
	if report.Version != saved.Version || report.TestReport == nil || !report.TestReport.Passed {
		t.Fatal("report observation changed draft")
	}
	form.Set("sample", `{"input":{"unsaved":true}}`)
	for _, action := range []string{"run-tests", "publish"} {
		denied := consolePost(e, cookie, page, base+"/"+action, form)
		if denied.Code != 400 || !strings.Contains(denied.Body.String(), "Save your current sample") || !strings.Contains(denied.Body.String(), "Results are outdated") {
			t.Fatalf("unsaved %s: %d %s", action, denied.Code, denied.Body.String())
		}
	}
	form.Set("sample", stringMustJSON(t, saved.TestSuite.Sample))
	page = consolePost(e, cookie, page, base+"/publish", form)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "Results are current") {
		t.Fatalf("publish invalidated matching report: %d %s", page.Code, page.Body.String())
	}
	published := consoleVersion(t, e, app.Slug, draft.Name)
	form.Set("expected_version", strconv.FormatInt(published.Version, 10))
	def := published.Draft
	def.Steps[0].Path = "/changed"
	form.Set("definition", stringMustJSON(t, def))
	page = consolePost(e, cookie, page, base+"/save", form)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "Results are outdated") {
		t.Fatal("definition edit did not mark report outdated")
	}
	changed := consoleVersion(t, e, app.Slug, draft.Name)
	form.Set("expected_version", strconv.FormatInt(changed.Version, 10))
	denied := consolePost(e, cookie, page, base+"/clear-tests", form)
	if denied.Code != 400 {
		t.Fatal("clear accepted without confirmation")
	}
	form.Set("confirm_clear_tests", "yes")
	page = consolePost(e, cookie, denied, base+"/clear-tests", form)
	cleared := consoleVersion(t, e, app.Slug, draft.Name)
	if page.Code != 200 || cleared.TestSuite != nil || cleared.TestReport != nil {
		t.Fatal("clear retained saved data")
	}
	runs, _, _ := (*e.Store()).ListWorkflowRuns(t.Context(), app.ID, state.ListWorkflowRunsOpts{})
	if len(runs) != 0 {
		t.Fatal("saved tests started real actions")
	}
}
func stringMustJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
func TestAutomationConsoleSavedTestsAPIFences(t *testing.T) {
	e, app, cookie := consoleFixture(t)
	base := "/dashboard/apps/" + app.Slug + "/automations"
	page := apidserver.RestartProgressDashboardGET(e, cookie, base+"?template=scheduled-report")
	form := consoleForm(t, "api-cases")
	page = consolePost(e, cookie, page, base+"/save", form)
	if page.Code != 200 {
		t.Fatal("save draft")
	}
	draft := consoleVersion(t, e, app.Slug, "api-cases")
	path := "/v1/apps/" + app.Slug + "/automations/" + draft.Name + "/tests"
	unknown := map[string]any{"expected_version": draft.Version, "suite": map[string]any{"sample": map[string]any{"definition": draft.Draft}}}
	if got := e.Do(t, http.MethodPut, path, unknown, nil); got.Code != 400 {
		t.Fatal("saved definition authority accepted")
	}
	if got := e.Do(t, http.MethodPut, path, api.SaveAutomationTestSuiteRequest{ExpectedVersion: draft.Version + 100}, nil); got.Code != 409 {
		t.Fatalf("stale save: %d", got.Code)
	}
	if got := e.Do(t, http.MethodPost, path+"/run", api.RunAutomationTestsRequest{ExpectedVersion: draft.Version}, nil); got.Code != 422 {
		t.Fatalf("missing cases accepted: %d", got.Code)
	}
	form.Set("expected_version", strconv.FormatInt(draft.Version, 10))
	missingCSRF := consolePost(e, cookie, apidserver.RestartProgressDashboardGET(e, cookie, base+"?name="+draft.Name), base+"/save-tests", form)
	if missingCSRF.Code != 200 {
		t.Fatalf("save tests: %d", missingCSRF.Code)
	}
	old := consolePost(e, cookie, missingCSRF, base+"/save-tests", form)
	if old.Code != 409 {
		t.Fatalf("stale browser save: %d", old.Code)
	}
	other, err := (*e.Store()).CreateAccount(t.Context(), "saved-tests-other@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	denied := apidserver.RestartProgressDashboardGET(e, apidserver.RestartProgressDashboardCookie(t, e, other.ID, false), base+"?name="+draft.Name)
	if denied.Code != 404 {
		t.Fatal("cross-account saved data read accepted")
	}
}
