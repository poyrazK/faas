// adr: 1287
package apidserver_test

import (
	"encoding/json"
	"html"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAutomationConsoleGuidedReportJourney(t *testing.T) {
	e, app, cookie := consoleFixture(t)
	base := "/dashboard/apps/" + app.Slug + "/automations"
	page := apidserver.RestartProgressDashboardGET(e, cookie, base+"?template=scheduled-report")
	form := consoleForm(t, "guided-report")
	form.Set("editor_mode", "report")
	form.Set("report_schedule", "30 8 * * 1-5")
	form.Set("report_timezone", "Europe/Istanbul")
	form.Set("report_collect", "/reports/collect")
	form.Set("report_deliver", "/reports/deliver")
	form.Set("report_overlap", "skip")
	form.Set("report_catch_up", "latest")
	form.Set("report_window", "2h")
	page = consolePost(e, cookie, page, base+"/preview-schedule", form)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Edited definition preview") || !strings.Contains(html.UnescapeString(page.Body.String()), "+03:00") {
		t.Fatalf("preview: %d %s", page.Code, page.Body.String())
	}
	rows, _ := (*e.Store()).ListAutomations(t.Context(), app.ID)
	runs, _, _ := (*e.Store()).ListWorkflowRuns(t.Context(), app.ID, state.ListWorkflowRunsOpts{})
	if len(rows) != 0 || len(runs) != 0 {
		t.Fatal("preview persisted intent")
	}
	page = consolePost(e, cookie, page, base+"/save", form)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Not published") {
		t.Fatalf("save: %d %s", page.Code, page.Body.String())
	}
	saved := consoleVersion(t, e, app.Slug, "guided-report")
	if saved.Draft.Trigger.Schedule != "30 8 * * 1-5" || saved.Draft.Trigger.Timezone != "Europe/Istanbul" ||
		saved.Draft.Steps[0].Path != "/reports/collect" || saved.Draft.Steps[1].Path != "/reports/deliver" ||
		saved.Published != nil || saved.Draft.Trigger.Enabled == nil || *saved.Draft.Trigger.Enabled {
		t.Fatalf("configured draft mismatch: %+v", saved)
	}
	form.Set("expected_version", strconv.FormatInt(saved.Version, 10))
	form.Set("report_schedule", "0 10 * * 1-5")
	stale := consolePost(e, cookie, page, base+"/publish", form)
	if stale.Code != http.StatusConflict {
		t.Fatalf("unsaved form settings published: %d %s", stale.Code, stale.Body.String())
	}
	form.Set("report_schedule", "30 8 * * 1-5")
	page = consolePost(e, cookie, stale, base+"/publish", form)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Published schedule preview") {
		t.Fatalf("publish: %d %s", page.Code, page.Body.String())
	}
	published := consoleVersion(t, e, app.Slug, "guided-report")
	if published.Published == nil || published.Enabled {
		t.Fatal("publishing enabled the scheduled report")
	}
	form.Set("expected_version", strconv.FormatInt(published.Version, 10))
	form.Set("enabled", "true")
	enabled := consolePost(e, cookie, page, base+"/enabled", form)
	if enabled.Code != http.StatusSeeOther || !consoleVersion(t, e, app.Slug, "guided-report").Enabled {
		t.Fatalf("explicit enable: %d %s", enabled.Code, enabled.Body.String())
	}
	// An unsaved preview must show its edited schedule separately from the
	// published one, and must not alter the published definition.
	form.Set("report_schedule", "0 10 * * 1-5")
	page = apidserver.RestartProgressDashboardGET(e, cookie, enabled.Header().Get("Location"))
	page = consolePost(e, cookie, page, base+"/preview-schedule", form)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Edited definition preview") || !strings.Contains(page.Body.String(), "Published schedule preview") ||
		!strings.Contains(page.Body.String(), "30 8 * * 1-5") || !strings.Contains(page.Body.String(), "0 10 * * 1-5") {
		t.Fatalf("preview sources: %d %s", page.Code, page.Body.String())
	}
	current := consoleVersion(t, e, app.Slug, "guided-report")
	if current.Published.Trigger.Schedule != "30 8 * * 1-5" {
		t.Fatal("preview changed the published schedule")
	}
}

func TestAutomationConsoleInvalidReportSettingsAndReadiness(t *testing.T) {
	e, app, cookie := consoleFixture(t)
	base := "/dashboard/apps/" + app.Slug + "/automations"
	for _, test := range []struct{ field, value string }{
		{"report_schedule", "not cron"}, {"report_timezone", "Not/AZone"}, {"report_collect", "https://example.com"},
		{"report_deliver", ""}, {"report_window", "25h"},
	} {
		form := consoleForm(t, "invalid-report")
		form.Set("editor_mode", "report")
		form.Set("report_schedule", "0 9 * * *")
		form.Set("report_timezone", "UTC")
		form.Set("report_collect", "/collect")
		form.Set("report_deliver", "/deliver")
		form.Set("report_overlap", "skip")
		form.Set("report_catch_up", "latest")
		form.Set(test.field, test.value)
		page := apidserver.RestartProgressDashboardGET(e, cookie, base+"?template=scheduled-report")
		page = consolePost(e, cookie, page, base+"/preview-schedule", form)
		if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Fix these issues before publishing") ||
			strings.Contains(page.Body.String(), "Edited definition preview</h3>") {
			t.Fatalf("invalid %s previewed: %d %s", test.field, page.Code, page.Body.String())
		}
	}
	other, err := (*e.Store()).CreateApp(t.Context(), state.App{AccountID: app.AccountID, Slug: "undeployed-report", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	page := apidserver.RestartProgressDashboardGET(e, cookie, "/dashboard/apps/"+other.Slug+"/automations?template=scheduled-report")
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "No live default deployment is available") ||
		!strings.Contains(page.Body.String(), "Validation and mocked scenarios do not call handlers") {
		t.Fatalf("readiness: %d %s", page.Code, page.Body.String())
	}
	(*e.S()).WithWorkflowRuntimeEnabled(false)
	page = apidserver.RestartProgressDashboardGET(e, cookie, base+"?template=scheduled-report")
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "deployment readiness remains unverified") {
		t.Fatalf("blocked readiness: %d %s", page.Code, page.Body.String())
	}
	response := e.Do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/automations", nil, nil)
	var listing api.ListAutomationsResponse
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &listing) != nil || len(listing.Automations) != 0 {
		t.Fatal("invalid settings persisted a draft")
	}
}
