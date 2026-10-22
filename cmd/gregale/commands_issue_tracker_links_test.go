package main_test

import (
	"bytes"
	"encoding/json"
	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIssueTrackerLinksCLIJourney(t *testing.T) {
	issue := "11111111-1111-4111-8111-111111111111"
	linkID := "22222222-2222-4222-8222-222222222222"
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		base := "/v1/apps/payments/issues/" + issue + "/tracker-links"
		switch r.Method {
		case "POST":
			if r.URL.Path != base {
				t.Errorf("path: %s", r.URL)
			}
			var in api.PutIssueTrackerLinkRequest
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.URL != "https://github.com/owner/repo/issues/42" {
				t.Errorf("canonical request: %+v %v", in, err)
			}
			_ = json.NewEncoder(w).Encode(api.IssueTrackerLink{ID: linkID, URL: in.URL, Title: in.Title})
		case "GET":
			if r.URL.Path != base || r.URL.Query().Get("cursor") != "next+/=" {
				t.Errorf("list: %s", r.URL)
			}
			_ = json.NewEncoder(w).Encode(api.ListIssueTrackerLinksResponse{Items: []api.IssueTrackerLink{{ID: linkID}}, NextCursor: "tail"})
		case "DELETE":
			if r.URL.Path != base+"/"+linkID {
				t.Errorf("delete: %s", r.URL)
			}
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	previousOut, previousJSON := (*main.OsStdout), (*main.JsonOutput)
	var out bytes.Buffer
	(*main.OsStdout), (*main.JsonOutput) = &out, true
	t.Cleanup(func() { (*main.OsStdout), (*main.JsonOutput) = previousOut, previousJSON })
	for _, args := range [][]string{
		{"links", "save", "--app", "payments", "--issue", issue, "--url", "https://GitHub.com/OWNER/Repo/issues/0042?x=y", "--title", "Fix checkout"},
		{"links", "list", "--app", "payments", "--issue", issue, "--cursor", "next+/="},
		{"links", "delete", "--app", "payments", "--issue", issue, "--link", linkID},
	} {
		out.Reset()
		if main.CmdIssues(args) != 0 {
			t.Fatalf("failed: %v", args)
		}
		if !json.Valid(out.Bytes()) {
			t.Fatalf("invalid JSON: %s", out.String())
		}
	}
	if main.CmdIssues([]string{"links", "save", "--app", "payments", "--issue", issue, "--url", "javascript:attack"}) == 0 {
		t.Fatal("unsafe URL accepted")
	}
	if main.CmdIssues([]string{"links", "list", "--app", "payments", "--issue", issue, "--title", "ignored"}) == 0 {
		t.Fatal("ignored mutation flag")
	}
	if calls != 3 {
		t.Fatalf("invalid requests reached server: %d", calls)
	}
}
