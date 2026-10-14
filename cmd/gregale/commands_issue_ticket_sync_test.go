package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/issuetracker"
)

type syncCLIProvider struct {
	status          issuetracker.Status
	writes          int
	reads           int
	changeOnRecheck bool
}

func (p *syncCLIProvider) ReadStatus(context.Context, string, string) (issuetracker.Status, error) {
	p.reads++
	out := p.status
	if p.changeOnRecheck && p.reads > 1 {
		out.State = "open"
	}
	return out, nil
}
func (p *syncCLIProvider) SetStatus(_ context.Context, observed issuetracker.Status, _ string, state, _ string) (issuetracker.Status, error) {
	p.writes++
	p.status.State = state
	return p.status, nil
}
func TestIssueTicketSyncPreviewPullRequiresDeploymentAndIsIdempotent(t *testing.T) {
	appID := "22222222-2222-4222-8222-222222222222"
	issueID := "33333333-3333-4333-8333-333333333333"
	linkID := "55555555-5555-4555-8555-555555555555"
	deploymentID := "66666666-6666-4666-8666-666666666666"
	state := "open"
	actions := 0
	linkPages := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "POST" {
			actions++
			var action api.IssueActionRequest
			_ = json.NewDecoder(r.Body).Decode(&action)
			if action.Action != "resolve" || action.FixedDeploymentID != deploymentID {
				t.Errorf("resolution lost deployment evidence: %+v", action)
			}
			state = "resolved"
			_ = json.NewEncoder(w).Encode(api.Issue{ID: issueID, AppID: appID, State: state})
			return
		}
		switch {
		case r.URL.Path == "/v1/apps/payments":
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: appID, Slug: "payments"})
		case strings.HasSuffix(r.URL.Path, "/tracker-links"):
			linkPages++
			if r.URL.Query().Get("cursor") == "" {
				_ = json.NewEncoder(w).Encode(api.ListIssueTrackerLinksResponse{Items: []api.IssueTrackerLink{{ID: "77777777-7777-4777-8777-777777777777"}}, NextCursor: "next"})
				return
			}
			_ = json.NewEncoder(w).Encode(api.ListIssueTrackerLinksResponse{Items: []api.IssueTrackerLink{{ID: linkID, Provider: "github", URL: "https://github.com/owner/repo/issues/42"}}})
		default:
			_ = json.NewEncoder(w).Encode(api.IssueDetail{Issue: api.Issue{ID: issueID, AppID: appID, State: state}})
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	t.Setenv("GREGALE_GITHUB_TOKEN", "secret")
	previousOut, previousJSON := osStdout, jsonOutput
	var out bytes.Buffer
	osStdout, jsonOutput = &out, true
	t.Cleanup(func() { osStdout, jsonOutput = previousOut, previousJSON })
	provider := &syncCLIProvider{status: issuetracker.Status{Provider: "github", URL: "https://github.com/owner/repo/issues/42", ID: "42", State: "resolved"}}
	args := []string{"--app", "payments", "--issue", issueID, "--link", linkID, "--direction", "pull"}
	if runIssueTicketSync(args, provider) != 0 {
		t.Fatal("preview failed")
	}
	var preview issueTicketSyncResult
	if err := json.Unmarshal(out.Bytes(), &preview); err != nil || !preview.NeedsDeployment || !preview.WouldChange || preview.Applied {
		t.Fatal("preview lost required deployment", out.String(), err)
	}
	if actions != 0 || provider.writes != 0 {
		t.Fatal("preview mutated state")
	}
	if runIssueTicketSync(append(append([]string{}, args...), "--apply"), provider) == 0 || actions != 0 {
		t.Fatal("resolution applied without a deployment")
	}
	apply := append(append([]string{}, args...), "--apply", "--deployment", deploymentID)
	if runIssueTicketSync(apply, provider) != 0 || actions != 1 || state != "resolved" {
		t.Fatal("pull failed to resolve")
	}
	if runIssueTicketSync(apply, provider) != 0 || actions != 1 {
		t.Fatal("unchanged lifecycle repeated an action")
	}
	state = "open"
	provider.reads = 0
	provider.changeOnRecheck = true
	if runIssueTicketSync(apply, provider) == 0 || actions != 1 {
		t.Fatal("changed provider authority was pulled")
	}
	if linkPages < 6 || provider.writes != 0 {
		t.Fatal("pagination skipped reference or pull changed provider")
	}
}
func TestIssueTicketSyncPushRechecksGroupAndRefusesStaleSource(t *testing.T) {
	appID := "22222222-2222-4222-8222-222222222222"
	issueID := "33333333-3333-4333-8333-333333333333"
	linkID := "55555555-5555-4555-8555-555555555555"
	reads := 0
	stale := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != "GET" {
			t.Fatal("push wrote Gregale lifecycle")
		}
		switch {
		case r.URL.Path == "/v1/apps/payments":
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: appID, Slug: "payments"})
		case strings.HasSuffix(r.URL.Path, "/tracker-links"):
			_ = json.NewEncoder(w).Encode(api.ListIssueTrackerLinksResponse{Items: []api.IssueTrackerLink{{ID: linkID, Provider: "github", URL: "https://github.com/owner/repo/issues/42"}}})
		default:
			reads++
			id := issueID
			if stale && reads > 1 {
				id = "77777777-7777-4777-8777-777777777777"
			}
			_ = json.NewEncoder(w).Encode(api.IssueDetail{Issue: api.Issue{ID: id, AppID: appID, State: "open"}})
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	t.Setenv("GREGALE_GITHUB_TOKEN", "secret")
	previousOut, previousJSON := osStdout, jsonOutput
	var out bytes.Buffer
	osStdout, jsonOutput = &out, true
	t.Cleanup(func() { osStdout, jsonOutput = previousOut, previousJSON })
	provider := &syncCLIProvider{status: issuetracker.Status{Provider: "github", URL: "https://github.com/owner/repo/issues/42", ID: "42", State: "resolved"}}
	args := []string{"--app", "payments", "--issue", issueID, "--link", linkID, "--direction", "push", "--apply"}
	if runIssueTicketSync(args, provider) != 0 || provider.writes != 1 || provider.status.State != "open" {
		t.Fatal("push did not reopen provider")
	}
	stale = true
	reads = 0
	provider.status.State = "resolved"
	if runIssueTicketSync(args, provider) == 0 || provider.writes != 1 {
		t.Fatal("regrouped source was pushed")
	}
	if cmdIssues([]string{"tickets", "sync", "--app", "payments", "--issue", "invalid", "--link", linkID, "--direction", "push"}) == 0 {
		t.Fatal("invalid sync IDs accepted")
	}
}
