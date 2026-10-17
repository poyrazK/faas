package faas_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestIssueSavedViewSDKAppliesLatestViewerRelativeFilters(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/apps/payments/issue-saved-views/mine" {
			_ = json.NewEncoder(w).Encode(faas.IssueSavedView{Key: "mine", Filters: faas.IssueSavedViewFilters{State: "resolved", Assignee: "me", Sort: "impact", Tag: "component=payments"}})
			return
		}
		if r.URL.Path != "/v1/apps/payments/issues" || r.URL.Query().Get("assignee") != "me" || r.URL.Query().Get("state") != "resolved" || r.URL.Query().Get("tag") != "component=payments" || r.URL.Query().Get("cursor") != "next" {
			t.Errorf("wrong view application: %s", r.URL)
		}
		_ = json.NewEncoder(w).Encode(faas.ListIssuesResponse{Items: []faas.Issue{{ID: "one"}}})
	}))
	defer srv.Close()
	c, err := faas.NewClient(srv.URL, "fp_live_test")
	if err != nil {
		t.Fatal(err)
	}
	view, err := c.ListIssuesForSavedView(context.Background(), "payments", "mine", "next")
	if err != nil || len(view.Items) != 1 || calls != 2 {
		t.Fatalf("view request contract: %+v %v", view, err)
	}
}
