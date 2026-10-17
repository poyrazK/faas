// adr: 1290
package apidserver_test

import (
	"html"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestAutomationConsoleGuidedEditsAreLocalAndProtected(t *testing.T) {
	e, app, cookie := consoleFixture(t)
	base := "/dashboard/apps/" + app.Slug + "/automations"
	page := apidserver.RestartProgressDashboardGET(e, cookie, base+"?template=scheduled-report")
	form := consoleForm(t, "guided-edits")
	form.Set("test_editor_mode", "guided")
	page = consolePost(e, cookie, page, base+"/save", form)
	draft := consoleVersion(t, e, app.Slug, "guided-edits")
	form.Set("expected_version", strconv.FormatInt(draft.Version, 10))
	form.Set("test_editor_mode", "guided")
	form.Set("test_editor_source", "guided")
	form.Set("test_case_index", "0")
	form.Set("test_assertion_index", "0")
	form.Set("test_case_name", "<case name>")
	// Read the selected controls exactly as a browser submits them.
	for _, field := range []string{"test_case_simulation", "test_output"} {
		re := regexp.MustCompile(`<textarea[^>]*name="` + field + `"[^>]*>([\s\S]*?)</textarea>`)
		match := re.FindStringSubmatch(page.Body.String())
		if len(match) != 2 {
			t.Fatalf("missing %s", field)
		}
		form.Set(field, html.UnescapeString(match[1]))
	}
	form.Set("test_step", "collect")
	form.Set("test_state", "mocked")
	page = consolePost(e, cookie, page, base+"/apply-test-case", form)
	if page.Code != 200 || strings.Contains(page.Body.String(), `value="<case name>"`) || !strings.Contains(page.Body.String(), "&lt;case name&gt;") {
		t.Fatalf("local edit/escaping: %d", page.Code)
	}
	current := consoleVersion(t, e, app.Slug, draft.Name)
	if current.Version != draft.Version || current.TestSuite != nil {
		t.Fatal("local editor action mutated saved data")
	}
	bad := httptest.NewRequest(http.MethodPost, base+"/add-test-case", strings.NewReader(form.Encode()))
	bad.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	bad.AddCookie(cookie)
	denied := httptest.NewRecorder()
	(*e.H()).ServeHTTP(denied, bad)
	if denied.Code != http.StatusBadRequest {
		t.Fatalf("local editor action bypassed CSRF: %d", denied.Code)
	}
}
