// adr: 1303
package automationconsole

import (
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
)

func baselineFixture(t *testing.T) (*inspectionHost, url.Values, api.AutomationLiveSuiteComparison) {
	t.Helper()
	host, id := inspectionFixture(t)
	now := time.Now().UTC().Add(-time.Minute)
	summary := api.AutomationLiveSuiteSummary{ID: id, Name: "report", Version: 3, PublishedVersion: 2, DeploymentID: uuid.NewString(), CaseCount: 1, LinkedRuns: 1, CreatedAt: now, UpdatedAt: now}
	observation := &api.AutomationLiveSuiteObservation{RunID: uuid.NewString(), CreatedAt: &now, Result: "passed"}
	view := api.AutomationLiveSuiteComparison{Baseline: summary, Candidate: summary, ObservedAt: time.Now().UTC(), Summary: api.AutomationLiveSuiteComparisonCounts{Total: 1, Unchanged: 1}, Cases: []api.AutomationLiveSuiteCaseComparison{{Name: "steady", Change: "unchanged", Baseline: observation, Candidate: observation}}}
	host.responses["live-suite-compare"] = inspectionJSON(t, view)
	host.responses["live-suite-list"] = inspectionJSON(t, api.ListAutomationLiveSuitesResponse{Suites: []api.AutomationLiveSuiteSummary{summary}})
	host.responses["live-suite-get"] = inspectionJSON(t, api.AutomationLiveSuite{AutomationLiveSuiteSummary: summary})
	form := url.Values{"name": {"report"}, "suite_id": {id}, "baseline_name": {"production"}, "baseline_revision": {"0"}, "expected_version": {"3"}, "expected_published_version": {"2"}, "expected_deployment_id": {summary.DeploymentID}, "baseline_policy": {`{"version":2,"required_cases":["steady"],"max_candidate_age_seconds":3600}`}, "csrf_token": {"test-csrf"}}
	return host, form, view
}
func TestBaselinePreviewReadsEvidenceAndExplainsBlocks(t *testing.T) {
	for _, mode := range []string{"passed", "stale", "outdated", "missing", "unavailable"} {
		t.Run(mode, func(t *testing.T) {
			host, form, view := baselineFixture(t)
			switch mode {
			case "stale":
				old := time.Now().Add(-2 * time.Hour)
				view.Cases[0].Candidate.CreatedAt = &old
			case "outdated":
				view.CandidateOutdated = true
			case "missing":
				form.Set("baseline_policy", `{"version":2,"required_cases":["missing"],"max_candidate_age_seconds":3600}`)
			case "unavailable":
				view.Cases[0].Candidate.Result = "unavailable"
				view.Cases[0].Baseline.Result = "unavailable"
				view.Cases[0].Change = "unknown"
				view.Summary.Unchanged = 0
				view.Summary.Unknown = 1
			}
			host.responses["live-suite-compare"] = inspectionJSON(t, view)
			service := Service{Host: host, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
			r := reportRequest(form)
			r.SetPathValue("action", "preview-baseline")
			w := httptest.NewRecorder()
			service.Act(w, r)
			body := html.UnescapeString(w.Body.String())
			if w.Code != 200 || !strings.Contains(body, "Qualification:") {
				t.Fatalf("preview %d %s", w.Code, body)
			}

			explanation := map[string]string{"stale": "oldest run exceeds", "outdated": "historical automation target", "missing": "does not include every required case", "unavailable": "no longer current"}[mode]
			if explanation != "" && !strings.Contains(body, explanation) {
				t.Fatal("missing useful blocking explanation", mode)
			}
			confirmed := strings.Contains(body, "Confirm promotion / retry")
			if confirmed != (mode == "passed") {
				t.Fatal("incorrect qualification", mode, body)
			}
			for _, call := range host.calls {
				if call.Kind == "baseline-promote" || strings.Contains(call.Kind, "start") || strings.Contains(call.Kind, "check") {
					t.Fatal("preview mutated evidence", call.Kind)
				}
			}
		})
	}
}
func TestBaselinePromotionRequiresConfirmationAndPreservesRetryIntent(t *testing.T) {
	for _, mode := range []string{"unconfirmed", "conflict", "success", "wrong-receipt"} {
		t.Run(mode, func(t *testing.T) {
			host, form, view := baselineFixture(t)
			id := uuid.NewString()
			form.Set("promotion_id", id)
			if mode != "unconfirmed" {
				form.Set("confirm_baseline", "yes")
			}
			receipt := api.AutomationLiveBaseline{AppID: "app", Name: "report", BaselineName: "production", PromotionID: id, Revision: 1, Suite: view.Candidate, Policy: []byte(form.Get("baseline_policy"))}
			host.responses["baseline-promote"] = inspectionJSON(t, receipt)
			if mode == "conflict" {
				host.responses["baseline-promote"] = Response{Status: 409, Body: []byte(`{"detail":"PRIVATE"}`)}
			}
			if mode == "wrong-receipt" {
				receipt.Suite.DeploymentID = uuid.NewString()
				host.responses["baseline-promote"] = inspectionJSON(t, receipt)
			}
			service := Service{Host: host, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
			r := reportRequest(form)
			r.SetPathValue("action", "promote-baseline")
			w := httptest.NewRecorder()
			service.Act(w, r)
			promotions := 0
			for _, call := range host.calls {
				if call.Kind == "baseline-promote" {
					promotions++
					request := call.Body.(api.PromoteAutomationLiveBaselineRequest)
					if call.PromotionID != id || request.ExpectedRevision != 0 || request.SuiteID != view.Candidate.ID {
						t.Fatal("intent changed")
					}
				}
			}
			if mode == "unconfirmed" && promotions != 0 || mode != "unconfirmed" && promotions != 1 {
				t.Fatal("unexpected promotions", promotions)
			}
			if mode == "success" && (w.Code != 200 || !strings.Contains(w.Body.String(), "Promotion recorded")) {
				t.Fatal("success not shown", w.Code)
			}
			if mode == "conflict" || mode == "wrong-receipt" {
				wantStatus := 409
				if mode == "wrong-receipt" {
					wantStatus = 502
				}
				if w.Code != wantStatus || !strings.Contains(w.Body.String(), id) || strings.Contains(w.Body.String(), "PRIVATE") {
					t.Fatal("unsafe retry response", w.Code)
				}
			}
		})
	}
}
func TestBaselineBrowsingIsReadOnlyAndScoped(t *testing.T) {
	for _, mode := range []string{"valid", "foreign", "error", "invalid-name"} {
		t.Run(mode, func(t *testing.T) {
			host, _, view := baselineFixture(t)
			receipt := api.AutomationLiveBaseline{AppID: "app", Name: "report", BaselineName: "production", Revision: 2, Suite: view.Candidate}
			if mode == "foreign" {
				receipt.AppID = "foreign"
			}
			receipt.Suite.ID = uuid.NewString()
			host.responses["baseline-get"] = inspectionJSON(t, receipt)
			host.responses["baseline-history"] = inspectionJSON(t, api.AutomationLiveBaselineHistory{Promotions: []api.AutomationLiveBaseline{receipt}})
			if mode == "error" {
				host.responses["baseline-get"] = Response{Status: 503}
			}
			query := "/?baseline_name=production"
			if mode == "invalid-name" {
				query = "/?baseline_name=../PRIVATE"
			}
			data := dashboard.AutomationConsoleData{Name: "report", Version: 3, LiveSuite: &api.AutomationLiveSuite{AutomationLiveSuiteSummary: view.Candidate}}
			service := Service{Host: host}
			service.inspectBaseline(httptest.NewRequest(http.MethodGet, query, nil), Scope{AppID: "app", Slug: "demo"}, &data)
			if (data.Baseline.Head != nil) != (mode == "valid") || data.Baseline.Ready != (mode == "valid") {
				t.Fatal("foreign/unavailable baseline accepted")
			}
			if mode == "valid" && !strings.Contains(data.Baseline.Command, "gregale --non-interactive automations live gate") {
				t.Fatal("missing CI command")
			}
			for _, call := range host.calls {
				if call.Kind != "baseline-list" && call.Kind != "baseline-get" && call.Kind != "baseline-history" {
					t.Fatal("browsing mutated state")
				}
			}
		})
	}
}
func TestBaselineIntentRequiresExplicitRevisionAndStrictPolicy(t *testing.T) {
	for _, field := range []string{"baseline_revision", "suite_id", "expected_version", "expected_published_version", "expected_deployment_id", "baseline_policy", "baseline_name"} {
		_, form, _ := baselineFixture(t)
		form.Del(field)
		r := reportRequest(form)
		if _, err := baselineIntent(r, "report"); err == nil {
			t.Fatal("missing field accepted", field)
		}
	}
}

func TestBaselineIntentCanonicalizesExistingCompactTarget(t *testing.T) {
	_, form, _ := baselineFixture(t)
	canonical := form.Get("expected_deployment_id")
	form.Set("expected_deployment_id", strings.ReplaceAll(canonical, "-", ""))
	request, err := baselineIntent(reportRequest(form), "report")
	if err != nil || request.ExpectedDeploymentID != canonical {
		t.Fatal("compact UUID changed target identity", err)
	}
}
