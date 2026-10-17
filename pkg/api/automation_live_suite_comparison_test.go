// adr: 1298
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAutomationLiveSuiteComparisonClientReadsEscapedPair(t *testing.T) {
	created := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.EscapedPath() != "/v1/apps/billing/automations/case%2Freport/live-suites/candidate%2Fid/compare/baseline%2Fid" || r.Header.Get("Authorization") != "Bearer test-token" || r.ContentLength > 0 {
			t.Error("comparison lost direction, path segments, or read-only contract", r.Method, r.URL.EscapedPath())
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(AutomationLiveSuiteComparison{Baseline: AutomationLiveSuiteSummary{ID: "baseline/id"}, Candidate: AutomationLiveSuiteSummary{ID: "candidate/id"}, Summary: AutomationLiveSuiteComparisonCounts{Total: 1, Regressions: 1}, Cases: []AutomationLiveSuiteCaseComparison{{Name: "happy", Candidate: &AutomationLiveSuiteObservation{Result: "passed", CreatedAt: &created}}}})
	}))
	defer srv.Close()
	view, err := NewClient(srv.URL, "test-token").CompareAutomationLiveSuites(context.Background(), "billing", "case/report", "candidate/id", "baseline/id")
	if err != nil || view.Summary.Regressions != 1 || view.Baseline.ID != "baseline/id" || view.Candidate.ID != "candidate/id" {
		t.Fatal("comparison response lost direction or results", err)
	}
	if len(view.Cases) != 1 || view.Cases[0].Candidate.CreatedAt == nil || !view.Cases[0].Candidate.CreatedAt.Equal(created) {
		t.Fatal("original creation timestamp lost")
	}
}

func TestAutomationLiveSuiteComparisonClientRequiresFreshnessObservations(t *testing.T) {
	for _, mode := range []string{"both-current", "historical-baseline", "historical-candidate", "missing-baseline", "missing-candidate", "null-baseline", "null-candidate"} {
		t.Run(mode, func(t *testing.T) {
			fields := map[string]any{"baseline_outdated": false, "candidate_outdated": false}
			switch mode {
			case "historical-baseline":
				fields["baseline_outdated"] = true
			case "historical-candidate":
				fields["candidate_outdated"] = true
			case "missing-baseline":
				delete(fields, "baseline_outdated")
			case "missing-candidate":
				delete(fields, "candidate_outdated")
			case "null-baseline":
				fields["baseline_outdated"] = nil
			case "null-candidate":
				fields["candidate_outdated"] = nil
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(fields) }))
			defer srv.Close()
			view, err := NewClient(srv.URL, "token").CompareAutomationLiveSuites(t.Context(), "billing", "report", "candidate", "baseline")
			valid := mode == "both-current" || mode == "historical-baseline" || mode == "historical-candidate"
			if valid && (err != nil || view.BaselineOutdated != (mode == "historical-baseline") || view.CandidateOutdated != (mode == "historical-candidate")) {
				t.Fatal("freshness flags lost", view, err)
			}
			if !valid && err == nil {
				t.Fatal("missing or null freshness accepted")
			}
		})
	}
}
