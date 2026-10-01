package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestIssuesListAssigneeFilterAndTriageColumns(t *testing.T) {
	ownerID := "11111111-1111-4111-8111-111111111111"
	var query url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/exports/issues" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		query = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.ListIssuesResponse{Items: []api.Issue{{
			ID: "issue-1", State: "open", AssigneeAccountID: ownerID, EventCount: 7,
			RegressionCount: 2, LastSeenAt: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC), Title: "Export failed",
			Impact24h: &api.IssueImpactSummary{IdentifiedCustomers: 3, ObservedEvents: 5, UnattributedEvents: 1},
		}}})
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	oldOut, oldJSON := osStdout, jsonOutput
	var out bytes.Buffer
	osStdout, jsonOutput = &out, false
	t.Cleanup(func() { osStdout, jsonOutput = oldOut, oldJSON })

	if code := cmdIssues([]string{"list", "--app", "exports", "--assignee", "me", "--sort", "impact", "--min-customers", "2"}); code != 0 {
		t.Fatalf("cmdIssues returned %d", code)
	}
	if got := query.Get("assignee"); got != "me" {
		t.Fatalf("request assignee filter = %q, want me", got)
	}
	if got := query.Get("sort"); got != "impact" {
		t.Fatalf("request sort = %q, want impact", got)
	}
	if got := query.Get("min_customers"); got != "2" {
		t.Fatalf("request minimum customer filter = %q, want 2", got)
	}
	for _, want := range []string{"OWNER", "VERIFIED CUSTOMERS (24H)", "EVENTS (24H)", "UNATTRIBUTED (24H)", "EVENTS", "RECURRENCES", ownerID, "3", "5", "1", "7", "2", "Export failed"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("CLI output missing %q: %s", want, out.String())
		}
	}
}

func TestIssuesImpactAlertPolicyCLI(t *testing.T) {
	var method, path string
	var threshold int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		policy := api.IssueImpactAlertPolicy{WindowSeconds: int64(api.IssueImpactAlertWindow / time.Second)}
		if r.Method == http.MethodPut {
			var input api.UpdateIssueImpactAlertPolicyRequest
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Errorf("decode update: %v", err)
			}
			threshold = input.MinimumCustomers
			policy.MinimumCustomers = threshold
			policy.Enabled = threshold > 0
		} else {
			policy.MinimumCustomers = 4
			policy.Enabled = true
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(policy)
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	oldOut, oldJSON := osStdout, jsonOutput
	var out bytes.Buffer
	osStdout, jsonOutput = &out, false
	t.Cleanup(func() { osStdout, jsonOutput = oldOut, oldJSON })

	if code := cmdIssues([]string{"impact-alert", "--app", "exports"}); code != 0 {
		t.Fatalf("policy read returned %d", code)
	}
	if method != http.MethodGet || path != "/v1/apps/exports/issue-impact-alert-policy" || !strings.Contains(out.String(), "4 verified customers") {
		t.Fatalf("policy read request=%s %s output=%q", method, path, out.String())
	}
	out.Reset()
	if code := cmdIssues([]string{"impact-alert", "--app", "exports", "--min-customers", "6"}); code != 0 {
		t.Fatalf("policy update returned %d", code)
	}
	if method != http.MethodPut || path != "/v1/apps/exports/issue-impact-alert-policy" || threshold != 6 || !strings.Contains(out.String(), "6 verified customers") {
		t.Fatalf("policy update request=%s %s threshold=%d output=%q", method, path, threshold, out.String())
	}
	out.Reset()
	if code := cmdIssues([]string{"impact-alert", "--app", "exports", "--min-customers", "0"}); code != 0 {
		t.Fatalf("policy disable returned %d", code)
	}
	if threshold != 0 || !strings.Contains(out.String(), "disabled") {
		t.Fatalf("disable threshold=%d output=%q", threshold, out.String())
	}
}
