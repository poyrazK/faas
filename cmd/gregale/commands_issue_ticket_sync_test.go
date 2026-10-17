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

func TestIssueTicketSyncNativeKeepsCredentialsOnServer(t *testing.T) {
	appID := "22222222-2222-4222-8222-222222222222"
	issueID := "33333333-3333-4333-8333-333333333333"
	linkID := "55555555-5555-4555-8555-555555555555"
	nativeWrites := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/native-status"):
			state := "resolved"
			if r.Method == "POST" {
				nativeWrites++
				var in api.UpdateNativeIssueTrackerStatusRequest
				_ = json.NewDecoder(r.Body).Decode(&in)
				if in.DesiredState != "open" || in.Expected.ID != "42" {
					t.Error("native push lost expected snapshot")
				}
				state = "open"
			}
			_ = json.NewEncoder(w).Encode(api.NativeIssueTrackerStatus{IssueID: issueID, LinkID: linkID, InstallationID: 42, Repository: "owner/repo", RepositoryID: 100, Status: api.IssueTrackerProviderStatus{Provider: "github", URL: "https://github.com/owner/repo/issues/42", ID: "42", State: state}})
		case r.URL.Path == "/v1/apps/payments":
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: appID, Slug: "payments"})
		case strings.HasSuffix(r.URL.Path, "/tracker-links"):
			_ = json.NewEncoder(w).Encode(api.ListIssueTrackerLinksResponse{Items: []api.IssueTrackerLink{{ID: linkID, Provider: "github", URL: "https://github.com/owner/repo/issues/42"}}})
		default:
			_ = json.NewEncoder(w).Encode(api.IssueDetail{Issue: api.Issue{ID: issueID, AppID: appID, State: "open"}})
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	t.Setenv("GREGALE_GITHUB_TOKEN", "")
	previousOut, previousJSON := osStdout, jsonOutput
	var out bytes.Buffer
	osStdout, jsonOutput = &out, true
	t.Cleanup(func() { osStdout, jsonOutput = previousOut, previousJSON })
	args := []string{"--app", "payments", "--issue", issueID, "--link", linkID, "--direction", "push", "--native", "--apply"}
	if runIssueTicketSync(args, nil) != 0 || nativeWrites != 1 {
		t.Fatal("native push required a local credential")
	}
	if runIssueTicketSync(append(args, "--token-env", "SHOULD_NOT_BE_READ"), nil) == 0 || nativeWrites != 1 {
		t.Fatal("native mode accepted mixed credentials")
	}
}

func TestIssueTicketSyncNativeLinearPushPullAndPreviewFences(t *testing.T) {
	const appID = "22222222-2222-4222-8222-222222222222"
	const issueID = "33333333-3333-4333-8333-333333333333"
	const linkID = "55555555-5555-4555-8555-555555555555"
	const workflow = "66666666-6666-4666-8666-666666666666"
	const deployment = "77777777-7777-4777-8777-777777777777"
	remote := api.NativeLinearIssueTrackerStatus{IssueID: issueID, LinkID: linkID, ConnectionRevision: 4, GroupFingerprint: strings.Repeat("a", 64), WorkspaceID: appID, ViewerID: issueID, TeamID: linkID, Status: api.IssueTrackerProviderStatus{Provider: "linear", URL: "https://linear.app/acme/issue/PAY-42", ID: appID, State: "resolved", TeamID: linkID, StateID: workflow}}
	pushes, actions, reads := 0, 0, 0
	race := false
	state := "open"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/native-linear-status"):
			if r.Method == "POST" {
				pushes++
				var in api.UpdateNativeLinearIssueTrackerStatusRequest
				_ = json.NewDecoder(r.Body).Decode(&in)
				if in.ExpectedConnectionRevision != 4 || in.ExpectedGroupFingerprint != strings.Repeat("a", 64) || in.LinearStateID != workflow || in.Expected != remote.Status {
					t.Error("Linear preview or state selection lost")
				}
				remote.Status.State = "open"
			} else {
				reads++
				if race && reads > 1 {
					remote.GroupFingerprint = strings.Repeat("b", 64)
				}
			}
			_ = json.NewEncoder(w).Encode(remote)
		case r.URL.Path == "/v1/apps/payments":
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: appID, Slug: "payments"})
		case strings.HasSuffix(r.URL.Path, "/tracker-links"):
			_ = json.NewEncoder(w).Encode(api.ListIssueTrackerLinksResponse{Items: []api.IssueTrackerLink{{ID: linkID, Provider: "linear", URL: remote.Status.URL}}})
		case strings.HasSuffix(r.URL.Path, "/actions"):
			actions++
			var in api.IssueActionRequest
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in.FixedDeploymentID != deployment || in.Action != "resolve" {
				t.Error("pull lost fixing deployment")
			}
			state = "resolved"
			_ = json.NewEncoder(w).Encode(api.Issue{ID: issueID, State: state})
		default:
			_ = json.NewEncoder(w).Encode(api.IssueDetail{Issue: api.Issue{ID: issueID, AppID: appID, State: state}})
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	t.Setenv("GREGALE_LINEAR_TOKEN", "")
	previousOut, previousJSON := osStdout, jsonOutput
	var out bytes.Buffer
	osStdout, jsonOutput = &out, true
	t.Cleanup(func() { osStdout, jsonOutput = previousOut, previousJSON })
	base := []string{"--app", "payments", "--issue", issueID, "--link", linkID, "--native"}
	push := append(append([]string{}, base...), "--direction", "push")
	if code := runIssueTicketSync(push, nil); code != 0 || pushes != 0 {
		t.Fatal("preview mutated")
	}
	if code := runIssueTicketSync(append(push, "--apply"), nil); code == 0 || pushes != 0 {
		t.Fatal("implicit Linear state allowed")
	}
	if code := runIssueTicketSync(append(push, "--apply", "--linear-state", workflow), nil); code != 0 || pushes != 1 {
		t.Fatal("native Linear push failed", code)
	}
	remote.Status.State = "resolved"
	pull := append(append([]string{}, base...), "--direction", "pull", "--apply")
	if code := runIssueTicketSync(pull, nil); code == 0 || actions != 0 {
		t.Fatal("resolution omitted fixing deployment")
	}
	reads = 0
	race = true
	if code := runIssueTicketSync(append(pull, "--deployment", deployment), nil); code == 0 || actions != 0 {
		t.Fatal("changed group resolved")
	}
	reads = 0
	race = false
	remote.GroupFingerprint = strings.Repeat("a", 64)
	if code := runIssueTicketSync(append(pull, "--deployment", deployment), nil); code != 0 || actions != 1 {
		t.Fatal("native Linear pull failed", code)
	}
	if code := runIssueTicketSync(append(push, "--token-env", "LOCAL"), nil); code == 0 || pushes != 1 {
		t.Fatal("native Linear accepted local credential")
	}
}
