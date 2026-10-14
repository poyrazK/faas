// adr: 1285
// adr: 1286
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// Opt-in browser qualification uses the real HTTP/session/form stack; ordinary
// portable tests do not require Chromium or a JavaScript dependency installation.
func TestAutomationConsoleBrowserJourney(t *testing.T) {
	if os.Getenv("GREGALE_AUTOMATION_BROWSER_ACCEPTANCE") != "1" {
		t.Skip("set GREGALE_AUTOMATION_BROWSER_ACCEPTANCE=1 with Playwright and Chromium installed")
	}
	e, app, cookie := consoleFixture(t)
	consoleBrowserJourney(t, e, cookie, "/dashboard/apps/"+app.Slug+"/automations", "authoring")
}

func TestAutomationConsoleBrowserRecoveryJourney(t *testing.T) {
	if os.Getenv("GREGALE_AUTOMATION_BROWSER_ACCEPTANCE") != "1" {
		t.Skip("set GREGALE_AUTOMATION_BROWSER_ACCEPTANCE=1 with Playwright and Chromium installed")
	}
	e, app, cookie := consoleFixture(t)
	run := consoleFailedRun(t, e, app)
	consoleBrowserJourney(t, e, cookie, "/dashboard/apps/"+app.Slug+"/automations?name="+run.WorkflowName+"&run="+run.ID, "recovery")
}

func consoleBrowserJourney(t *testing.T, e apidserver.TestEnv, cookie *http.Cookie, path, mode string) {
	t.Helper()
	server := httptest.NewServer(*e.H())
	defer server.Close()
	script, err := filepath.Abs("testdata/automation-console.browser.cjs")
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := json.Marshal(map[string]string{"url": server.URL + path, "mode": mode, "cookie_name": cookie.Name, "cookie_value": cookie.Value})
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "browser-fixture.json")
	if err := os.WriteFile(file, fixture, 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), "node", script, file)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("browser journey failed: %v\n%s", err, output)
	}
}
