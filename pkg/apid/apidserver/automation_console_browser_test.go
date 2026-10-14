// adr: 1285
// adr: 1286
// adr: 1287
// adr: 1288
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestAutomationConsoleBrowserReportJourney(t *testing.T) {
	if os.Getenv("GREGALE_AUTOMATION_BROWSER_ACCEPTANCE") != "1" {
		t.Skip("set GREGALE_AUTOMATION_BROWSER_ACCEPTANCE=1 with Playwright and Chromium installed")
	}
	e, app, cookie := consoleFixture(t)
	consoleBrowserJourney(t, e, cookie, "/dashboard/apps/"+app.Slug+"/automations", "report")
}

func TestAutomationConsoleBrowserWebhookJourney(t *testing.T) {
	if os.Getenv("GREGALE_AUTOMATION_BROWSER_ACCEPTANCE") != "1" {
		t.Skip("set GREGALE_AUTOMATION_BROWSER_ACCEPTANCE=1 with Playwright and Chromium installed")
	}
	t.Cleanup(apidserver.WithTestRecipient(t))
	e, app, cookie := consoleFixture(t)
	endpoint := consoleWebhookEndpoint(t, e, app.Slug)
	consoleBrowserJourney(t, e, cookie, "/dashboard/apps/"+app.Slug+"/automations", "webhook", map[string]string{
		"webhook_url": endpoint.EndpointURL, "webhook_secret": consoleWebhookSecret})
}

func consoleBrowserJourney(t *testing.T, e apidserver.TestEnv, cookie *http.Cookie, path, mode string, extra ...map[string]string) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		(*e.H()).ServeHTTP(w, r)
		if mode == "webhook" && r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/hooks/") {
			// Advance fixture fanout after verified ingress, without executing guests.
			work, err := (*e.Store()).ClaimDuePublishedEvent(t.Context(), time.Now().UTC())
			if err == nil && len(work.RecipientSnapshot) == 1 {
				if _, err := (*e.Store()).AdmitEventWorkflow(t.Context(), work.ID, work.ClaimToken, work.RecipientSnapshot[0].ID); err != nil {
					t.Errorf("admit browser webhook fixture: %v", err)
				}
			}
		}
	}))
	defer server.Close()
	script, err := filepath.Abs("testdata/automation-console.browser.cjs")
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]string{"url": server.URL + path, "mode": mode, "cookie_name": cookie.Name, "cookie_value": cookie.Value}
	for _, values := range extra {
		for name, value := range values {
			fields[name] = value
		}
	}
	fixture, err := json.Marshal(fields)
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
