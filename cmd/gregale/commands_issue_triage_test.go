package main

import (
	"bytes"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIssueTriageCLIComparisonAndExplicitBatch(t *testing.T) {
	baseline, candidate, id := uuid.NewString(), uuid.NewString(), uuid.NewString()
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/apps/payments/issue-release-comparison":
			if r.Method != "GET" || r.URL.Query().Get("baseline") != baseline || r.URL.Query().Get("candidate") != candidate || r.URL.Query().Get("environment") != "application" {
				t.Error("incorrect comparison request")
			}
			_ = json.NewEncoder(w).Encode(api.IssueReleaseComparison{})
		case "/v1/apps/payments/issues/bulk-actions":
			var in api.BulkIssueActionRequest
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil || r.Method != "POST" || len(in.IssueIDs) != 1 || in.IssueIDs[0] != id || in.Action.Action != "assign" {
				t.Error("incorrect batch request")
			}
			_ = json.NewEncoder(w).Encode(api.BulkIssueActionResponse{Items: []api.Issue{{ID: id}}})
		default:
			t.Error("unexpected route")
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	oldOut, oldJSON := osStdout, jsonOutput
	var out bytes.Buffer
	osStdout, jsonOutput = &out, false
	t.Cleanup(func() { osStdout, jsonOutput = oldOut, oldJSON })
	if cmdIssues([]string{"compare-releases", "--app", "payments", "--baseline", baseline, "--candidate", candidate}) != 0 {
		t.Fatal("comparison failed")
	}
	if !strings.Contains(out.String(), "Unknown") {
		t.Fatal("missing traffic displayed as zero health")
	}
	if cmdIssues([]string{"bulk", "--app", "payments", "--issues", id, "--action", "assign"}) != 0 {
		t.Fatal("batch failed")
	}
	if cmdIssues([]string{"bulk", "--app", "payments", "--issues", id + "," + id, "--action", "resolve", "--deployment", candidate}) == 0 {
		t.Fatal("duplicate selection accepted")
	}
	if requests != 2 || !strings.Contains(out.String(), "Updated 1 canonical") {
		t.Fatal("invalid selection reached server or output missing")
	}
}
