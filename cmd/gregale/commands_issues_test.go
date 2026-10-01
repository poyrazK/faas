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
		}}})
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	oldOut, oldJSON := osStdout, jsonOutput
	var out bytes.Buffer
	osStdout, jsonOutput = &out, false
	t.Cleanup(func() { osStdout, jsonOutput = oldOut, oldJSON })

	if code := cmdIssues([]string{"list", "--app", "exports", "--assignee", "me"}); code != 0 {
		t.Fatalf("cmdIssues returned %d", code)
	}
	if got := query.Get("assignee"); got != "me" {
		t.Fatalf("request assignee filter = %q, want me", got)
	}
	for _, want := range []string{"OWNER", "EVENTS", "RECURRENCES", ownerID, "7", "2", "Export failed"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("CLI output missing %q: %s", want, out.String())
		}
	}
}
