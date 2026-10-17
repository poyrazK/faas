package faas_test

import (
	"context"
	"encoding/json"
	faas "github.com/poyrazK/faas/sdk/go"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIssueTrackerLinkSDKContract(t *testing.T) {
	calls := 0
	link := faas.IssueTrackerLink{ID: "link-one", OriginalIssueID: "original", Provider: "github", ExternalKey: "owner/repo#42", URL: "https://github.com/owner/repo/issues/42", Title: "Fix checkout"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		base := "/v1/apps/payments/issues/group/tracker-links"
		switch r.Method {
		case "POST":
			if r.URL.Path != base {
				t.Errorf("save path: %s", r.URL)
			}
			var in faas.PutIssueTrackerLinkRequest
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.URL != link.URL || in.Title != link.Title {
				t.Errorf("save payload: %+v %v", in, err)
			}
			_ = json.NewEncoder(w).Encode(link)
		case "GET":
			if r.URL.Path != base || r.URL.Query().Get("cursor") != "next+/=" {
				t.Errorf("list request: %s", r.URL)
			}
			_ = json.NewEncoder(w).Encode(faas.ListIssueTrackerLinksResponse{Items: []faas.IssueTrackerLink{link}, NextCursor: "tail"})
		case "DELETE":
			if r.URL.Path != base+"/link-one" {
				t.Errorf("delete path: %s", r.URL)
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected method: %s", r.Method)
		}
	}))
	defer srv.Close()
	c, err := faas.NewClient(srv.URL, "fp_live_test")
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.PutIssueTrackerLink(context.Background(), "payments", "group", faas.PutIssueTrackerLinkRequest{URL: link.URL, Title: link.Title})
	if err != nil || got.ID != link.ID || got.OriginalIssueID != "original" {
		t.Fatalf("save: %+v %v", got, err)
	}
	page, err := c.ListIssueTrackerLinks(context.Background(), "payments", "group", "next+/=")
	if err != nil || len(page.Items) != 1 || page.NextCursor != "tail" {
		t.Fatalf("list: %+v %v", page, err)
	}
	if err := c.DeleteIssueTrackerLink(context.Background(), "payments", "group", link.ID); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("calls: %d", calls)
	}
}
