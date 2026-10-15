// adr: 1298
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAutomationLiveSuiteComparisonClientReadsEscapedPair(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.EscapedPath() != "/v1/apps/billing/automations/case%2Freport/live-suites/candidate%2Fid/compare/baseline%2Fid" || r.Header.Get("Authorization") != "Bearer test-token" || r.ContentLength > 0 {
			t.Error("comparison lost direction, path segments, or read-only contract", r.Method, r.URL.EscapedPath())
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(AutomationLiveSuiteComparison{Baseline: AutomationLiveSuiteSummary{ID: "baseline/id"}, Candidate: AutomationLiveSuiteSummary{ID: "candidate/id"}, Summary: AutomationLiveSuiteComparisonCounts{Total: 1, Regressions: 1}})
	}))
	defer srv.Close()
	view, err := NewClient(srv.URL, "test-token").CompareAutomationLiveSuites(context.Background(), "billing", "case/report", "candidate/id", "baseline/id")
	if err != nil || view.Summary.Regressions != 1 || view.Baseline.ID != "baseline/id" || view.Candidate.ID != "candidate/id" {
		t.Fatal("comparison response lost direction or results", err)
	}
}
