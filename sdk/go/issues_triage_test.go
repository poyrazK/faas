package faas_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestIssueTriageClientPreservesUnknownHealthAndBatchSelection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/apps/payments/issue-release-comparison":
			if r.URL.Query().Get("baseline") != "base" || r.URL.Query().Get("candidate") != "candidate" || r.URL.Query().Get("environment") != "application" {
				t.Error("comparison scope lost")
			}
			_ = json.NewEncoder(w).Encode(faas.IssueReleaseComparison{Candidate: faas.IssueReleaseHealth{RequestCoverage: "unknown"}})
		case "/v1/apps/payments/issues/bulk-actions":
			var in faas.BulkIssueActionRequest
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil || len(in.IssueIDs) != 2 || in.IssueIDs[1] != "two" || in.Action.Action != "reopen" {
				t.Error("batch changed")
			}
			_ = json.NewEncoder(w).Encode(faas.BulkIssueActionResponse{Items: []faas.Issue{{ID: "one"}, {ID: "two"}}})
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	c, err := faas.NewClient(srv.URL, "fp_live_test")
	if err != nil {
		t.Fatal(err)
	}
	out, err := c.CompareIssueReleases(context.Background(), "payments", "base", "candidate", "application")
	if err != nil || out.Candidate.HTTP5xxRate != nil || out.HTTP5xxRateDelta != nil || out.Candidate.RequestCoverage != "unknown" {
		t.Fatalf("invented health: %+v %v", out, err)
	}
	batch, err := c.BulkActOnIssues(context.Background(), "payments", faas.BulkIssueActionRequest{IssueIDs: []string{"one", "two"}, Action: faas.BulkIssueLifecycleAction{Action: "reopen"}})
	if err != nil || len(batch.Items) != 2 {
		t.Fatalf("batch response: %+v %v", batch, err)
	}
}
