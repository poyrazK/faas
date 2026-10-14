// adr: 1287
package automationconsole

import (
	"encoding/json"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/automationtemplates"
	"github.com/onebox-faas/faas/pkg/dashboard"
)

func reportFixture(t *testing.T) (dashboard.AutomationConsoleData, url.Values) {
	t.Helper()
	bundle, err := automationtemplates.Load("scheduled-report")
	if err != nil {
		t.Fatal(err)
	}
	data := dashboard.AutomationConsoleData{Name: "custom-report", Definition: pretty(bundle.Definition)}
	form := url.Values{"editor_mode": {"report"}, "report_schedule": {"30 8 * * 1-5"}, "report_timezone": {"Europe/Istanbul"},
		"report_collect": {"/reports/collect"}, "report_deliver": {"/reports/deliver"}, "report_overlap": {"allow"},
		"report_catch_up": {"latest"}, "report_window": {"2h"}}
	return data, form
}

func reportRequest(form url.Values) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return r
}

func TestReportSettingsPreserveAdvancedFieldsAndRequireExplicitActivation(t *testing.T) {
	data, form := reportFixture(t)
	var before api.WorkflowSpec
	if err := json.Unmarshal([]byte(data.Definition), &before); err != nil {
		t.Fatal(err)
	}
	enabled := true
	before.Trigger.Enabled = &enabled
	before.MaxConcurrentRuns = 3
	before.Steps[0].Input = json.RawMessage(`{"custom":"preserved"}`)
	before.Steps[0].Timeout = api.WorkflowScheduleCatchUpWindowDefault
	data.Definition = pretty(before)
	if message := prepareEditor(reportRequest(form), &data); message != "" {
		t.Fatal(message)
	}
	after, err := definition(data)
	if err != nil {
		t.Fatal(err)
	}
	if after.Name != "custom-report" || after.Trigger.Schedule != "30 8 * * 1-5" || after.Trigger.Timezone != "Europe/Istanbul" ||
		after.Steps[0].Path != "/reports/collect" || after.Steps[1].Path != "/reports/deliver" || after.Trigger.CatchUpWindow != "2h" ||
		after.Trigger.Overlap != "allow" || after.Trigger.CatchUp != "latest" {
		t.Fatalf("form settings were not applied: %s", data.Definition)
	}
	if after.MaxConcurrentRuns != before.MaxConcurrentRuns || pretty(after.Steps[0].Input) != pretty(before.Steps[0].Input) || after.Steps[0].Timeout != before.Steps[0].Timeout {
		t.Fatal("report settings discarded advanced options")
	}
	if after.Trigger.Enabled == nil || *after.Trigger.Enabled {
		t.Fatal("new guided report did not stay paused")
	}
	data.Version, data.Definition = 2, pretty(before)
	if message := prepareEditor(reportRequest(form), &data); message != "" {
		t.Fatal(message)
	}
	after, err = definition(data)
	if err != nil || after.Trigger.Enabled == nil || !*after.Trigger.Enabled {
		t.Fatal("editing an existing report changed its activation setting")
	}
}

func TestReportSettingsRejectDifferentGraphAndPreserveJSONSource(t *testing.T) {
	data, form := reportFixture(t)
	var spec api.WorkflowSpec
	if err := json.Unmarshal([]byte(data.Definition), &spec); err != nil {
		t.Fatal(err)
	}
	spec.Steps = append(spec.Steps, api.WorkflowStepSpec{Name: "archive", Path: "/archive"})
	data.Definition = pretty(spec)
	original := data.Definition
	if message := prepareEditor(reportRequest(form), &data); message == "" || data.Definition != original {
		t.Fatal("report settings accepted or replaced a different graph")
	}
	for _, mode := range []string{"json", ""} {
		form.Set("editor_mode", mode)
		if message := prepareEditor(reportRequest(form), &data); message != "" || data.Definition != original {
			t.Fatal("JSON editing applied stale report settings")
		}
	}
}

func TestSchedulePreviewDoesNotSaveOrStartAndKeepsSourcesDistinct(t *testing.T) {
	data, form := reportFixture(t)
	form.Set("name", data.Name)
	form.Set("definition", data.Definition)
	form.Set("expected_version", "0")
	host, _ := inspectionFixture(t)
	host.responses["validate"] = inspectionJSON(t, api.ValidateAutomationResponse{Valid: true})
	host.responses["get"] = Response{Status: http.StatusNotFound}
	service := Service{Host: host, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	request := reportRequest(form)
	request.SetPathValue("action", "preview-schedule")
	response := httptest.NewRecorder()
	service.Act(response, request)
	body := html.UnescapeString(response.Body.String())
	if response.Code != http.StatusOK || !strings.Contains(body, "Edited definition preview") || !strings.Contains(body, "+03:00") ||
		!strings.Contains(body, "Automatic starts in this definition: paused") || strings.Contains(body, "Published schedule preview</h3>") {
		t.Fatalf("draft preview: %d %s", response.Code, body)
	}
	for _, call := range host.calls {
		if call.Kind == "save" || call.Kind == "publish" || call.Kind == "enabled" || call.Kind == "run" || call.Kind == "schedule-preview" {
			t.Fatalf("draft preview requested %s", call.Kind)
		}
	}
}

func TestSetupChecksDoNotInferDeploymentOrHandlerReadiness(t *testing.T) {
	for _, test := range []struct {
		name string
		data dashboard.AutomationConsoleData
		want string
	}{
		{"unobserved", dashboard.AutomationConsoleData{}, "Unverified"},
		{"runtime off", dashboard.AutomationConsoleData{RuntimeObserved: true, UnavailableReason: "runtime_disabled"}, "Blocked"},
		{"no deployment", dashboard.AutomationConsoleData{RuntimeObserved: true, RuntimeEnabled: true, UnavailableReason: "deployment_unavailable"}, "Blocked"},
		{"eligible", dashboard.AutomationConsoleData{RuntimeObserved: true, RuntimeEnabled: true}, "Available"},
	} {
		t.Run(test.name, func(t *testing.T) {
			checks := setupChecks(test.data)
			if checks[1].Status != test.want || checks[2].Status != "Unverified" {
				t.Fatalf("misleading setup checks: %+v", checks)
			}
			if checks[3].Status != "Unavailable" || checks[4].Status != "Unavailable" {
				t.Fatal("missing publication metadata looked unpublished or paused")
			}
		})
	}
}

func TestPublishedScheduleReadFailureIsShownAsUnavailable(t *testing.T) {
	host, _ := inspectionFixture(t)
	bundle, err := automationtemplates.Load("scheduled-report")
	if err != nil {
		t.Fatal(err)
	}
	bundle.Definition.Name = "report"
	host.responses["get"] = inspectionJSON(t, api.AutomationResponse{
		Name: "report", Version: 2, Draft: bundle.Definition, Published: &bundle.Definition})
	service := Service{Host: host, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	response := httptest.NewRecorder()
	service.Show(response, httptest.NewRequest(http.MethodGet, "/?name=report", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Published schedule preview is temporarily unavailable") ||
		strings.Contains(response.Body.String(), "Published schedule preview</h3>") {
		t.Fatalf("failed preview looked successful: %d %s", response.Code, response.Body.String())
	}
}

func TestUnpublishedDraftDoesNotAppearEnabled(t *testing.T) {
	checks := setupChecks(dashboard.AutomationConsoleData{PublicationObserved: true, Enabled: true})
	if checks[4].Status != "Not published" {
		t.Fatalf("unpublished draft appeared active: %+v", checks[4])
	}
}

func TestReopenedReportPreservesOmittedDefaults(t *testing.T) {
	data, _ := reportFixture(t)
	var original api.WorkflowSpec
	if err := json.Unmarshal([]byte(data.Definition), &original); err != nil {
		t.Fatal(err)
	}
	original.Name = data.Name
	original.Trigger.Timezone, original.Trigger.Overlap, original.Trigger.CatchUp = "", "", ""
	data.Version, data.Definition = 3, pretty(original)
	settings := reportForm(original)
	form := url.Values{"editor_mode": {"report"}, "report_schedule": {settings.Schedule}, "report_timezone": {settings.Timezone}, "report_collect": {settings.CollectPath}, "report_deliver": {settings.DeliverPath}, "report_overlap": {settings.Overlap}, "report_catch_up": {settings.CatchUp}, "report_window": {settings.CatchUpWindow}}
	if message := prepareEditor(reportRequest(form), &data); message != "" {
		t.Fatal(message)
	}
	actual, err := definition(data)
	if err != nil || pretty(actual) != pretty(original) {
		t.Fatalf("unchanged report settings changed draft: %s", data.Definition)
	}
}
