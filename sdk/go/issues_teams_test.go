package faas_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestIssueTeamSDKManagementAndInbox(t *testing.T) {
	teamID := "11111111-1111-4111-8111-111111111111"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/apps/payments/issues" {
			if r.URL.Query().Get("team") != teamID || r.URL.Query().Get("state") != "open" || r.URL.Query().Get("cursor") != "next" {
				t.Error("team helper lost filters")
			}
			_ = json.NewEncoder(w).Encode(faas.ListIssuesResponse{Items: []faas.Issue{{ID: "issue", AssigneeTeamID: teamID}}})
			return
		}
		if r.Method == "PUT" {
			var in faas.PutIssueTeamRequest
			_ = json.NewDecoder(r.Body).Decode(&in)
			if len(in.MemberAccountIDs) != 1 {
				t.Error("SDK lost roster")
			}
		}
		_ = json.NewEncoder(w).Encode(faas.IssueTeam{ID: teamID, Key: "payments", Name: "Payments", MemberAccountIDs: []string{"member"}})
	}))
	defer srv.Close()
	c, err := faas.NewClient(srv.URL, "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.PutIssueTeam(context.Background(), "payments", "payments", faas.PutIssueTeamRequest{Name: "Payments", MemberAccountIDs: []string{"member"}}); err != nil {
		t.Fatal(err)
	}
	out, err := c.ListIssuesForTeam(context.Background(), "payments", "payments", "next")
	if err != nil || len(out.Items) != 1 || out.Items[0].AssigneeTeamID != teamID {
		t.Fatalf("team helper: %+v %v", out, err)
	}
}
