package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIssueOwnershipRulesClient(t *testing.T) {
	var method, path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		var rules IssueOwnershipRules
		if r.Method == http.MethodPut {
			if err := json.NewDecoder(r.Body).Decode(&rules); err != nil {
				t.Errorf("decode rules: %v", err)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(IssueOwnershipRules{Rules: []IssueOwnershipRule{{SourceKind: "worker", AssigneeAccountID: "account-1"}}})
	}))
	defer server.Close()
	client := NewClient(server.URL, "token")

	set, err := client.SetIssueOwnershipRules(context.Background(), "exports", IssueOwnershipRules{Rules: []IssueOwnershipRule{{RoutePrefix: "/exports", AssigneeAccountID: "account-1"}}})
	if err != nil || len(set.Rules) != 1 || method != http.MethodPut || path != "/v1/apps/exports/issue-ownership-rules" {
		t.Fatalf("set rules = %+v, %v, request %s %s", set, err, method, path)
	}
	got, err := client.GetIssueOwnershipRules(context.Background(), "exports")
	if err != nil || len(got.Rules) != 1 || got.Rules[0].SourceKind != "worker" || method != http.MethodGet {
		t.Fatalf("get rules = %+v, %v, request %s %s", got, err, method, path)
	}
}
