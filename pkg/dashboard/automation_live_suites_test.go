// adr: 1296
package dashboard_test

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
)

func TestSharedSuiteDashboardShowsObservedResultsAndEscapesNames(t *testing.T) {
	id := "00000000-0000-4000-8000-000000000010"
	run := "00000000-0000-4000-8000-000000000011"
	attack := "<script>unsafe-case</script>"
	for _, mode := range []string{"passed", "outdated", "unavailable"} {
		t.Run(mode, func(t *testing.T) {
			view := api.AutomationLiveSuite{AutomationLiveSuiteSummary: api.AutomationLiveSuiteSummary{ID: id, Name: "report", Version: 7, PublishedVersion: 4, DeploymentID: "deployment", CaseCount: 2, LinkedRuns: 1}, Outcome: mode, Outdated: mode == "outdated",
				Summary: api.AutomationLiveSuiteCounts{Selected: 2, NotStarted: 1}, Cases: []api.AutomationLiveSuiteCase{{Name: attack, RunID: run, Outcome: mode}, {Name: "second", Outcome: "not_started"}}}
			data := dashboard.AutomationConsoleData{Slug: "billing", Name: "report", Version: 7, BaseURL: "/dashboard/apps/billing/automations", LiveSuite: &view, LiveSuites: &api.ListAutomationLiveSuitesResponse{Suites: []api.AutomationLiveSuiteSummary{view.AutomationLiveSuiteSummary}}}
			w := httptest.NewRecorder()
			if err := dashboard.Render(w, slog.New(slog.NewTextHandler(io.Discard, nil)), "nonce", dashboard.Page{Body: "automations", Data: data}); err != nil {
				t.Fatal(err)
			}
			body := w.Body.String()
			for _, visible := range []string{"Shared live verification suites", "Suite result: " + mode, "&lt;script&gt;unsafe-case&lt;/script&gt;", "suite=" + id, "verification=" + run, "No run linked", "Refresh suite results"} {
				if !strings.Contains(body, visible) {
					t.Fatal("missing suite information", visible)
				}
			}
			if strings.Contains(body, attack) || strings.Contains(body, "formaction=\"/dashboard/apps/billing/automations/start-suite") {
				t.Fatal("suite view exposed markup or execution controls")
			}
			if mode == "outdated" && !strings.Contains(body, "has changed") {
				t.Fatal("outdated target was not explained")
			}
		})
	}
}
