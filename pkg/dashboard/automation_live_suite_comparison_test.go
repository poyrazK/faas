// adr: 1298
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

func TestSuiteComparisonDashboardEscapesEvidenceAndKeepsReadFormSeparate(t *testing.T) {
	baseline := api.AutomationLiveSuiteSummary{ID: "00000000-0000-4000-8000-000000000001", Name: "report"}
	candidate := baseline
	candidate.ID = "00000000-0000-4000-8000-000000000002"
	view := api.AutomationLiveSuiteComparison{Baseline: baseline, Candidate: candidate, BaselineOutdated: true, Summary: api.AutomationLiveSuiteComparisonCounts{Regressions: 1, Unknown: 1}, Cases: []api.AutomationLiveSuiteCaseComparison{
		{Name: "<script>unsafe-case</script>", Change: "regression", Baseline: &api.AutomationLiveSuiteObservation{RunID: "original-run", Result: "passed"}, Candidate: &api.AutomationLiveSuiteObservation{Result: "failed"}},
		{Name: "added-case", Change: "added", Candidate: &api.AutomationLiveSuiteObservation{Result: "not_started"}},
	}}
	data := dashboard.AutomationConsoleData{Slug: "billing", Name: "report", BaseURL: "/dashboard/apps/billing/automations", LiveSuite: &api.AutomationLiveSuite{AutomationLiveSuiteSummary: candidate}, LiveSuiteComparison: &view, LiveSuiteBaselineID: baseline.ID,
		LiveSuites: &api.ListAutomationLiveSuitesResponse{Suites: []api.AutomationLiveSuiteSummary{baseline, candidate}}}
	w := httptest.NewRecorder()
	if err := dashboard.Render(w, slog.New(slog.NewTextHandler(io.Discard, nil)), "nonce", dashboard.Page{Body: "automations", Data: data}); err != nil {
		t.Fatal(err)
	}
	body := w.Body.String()
	for _, visible := range []string{"Compare live suites", "1 regressions", "1 unknown", "historical target", "&lt;script&gt;unsafe-case&lt;/script&gt;", "Not selected", "not_started", "verification=original-run", "Swap baseline and candidate"} {
		if !strings.Contains(body, visible) {
			t.Fatal("missing comparison detail", visible)
		}
	}
	if strings.Contains(body, "<script>unsafe-case") {
		t.Fatal("case name was rendered as markup")
	}
	start := strings.Index(body, `<form method="get"`)
	if start < strings.Index(body, "</form>") {
		t.Fatal("comparison GET nested in the real-action editor form")
	}
	form := body[start : start+strings.Index(body[start:], "</form>")]
	if strings.Contains(form, "csrf_token") || strings.Contains(form, "definition") || strings.Contains(form, "confirm_") || !strings.Contains(form, `name="baseline"`) {
		t.Fatal("comparison query included editor data or action authorization")
	}
}
