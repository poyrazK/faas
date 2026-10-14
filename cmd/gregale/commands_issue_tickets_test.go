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

type ticketCLIProvider struct{ creates int }

func (p *ticketCLIProvider) Create(_ context.Context, r issuetracker.Request, token string) (issuetracker.Ticket, error) {
	p.creates++
	return issuetracker.Ticket{URL: "https://github.com/owner/repo/issues/42", ProviderID: "42"}, nil
}
func (p *ticketCLIProvider) Recover(context.Context, issuetracker.Request, string, string) (issuetracker.Ticket, error) {
	panic("unexpected recovery")
}
func TestIssueTicketCLIRetryResumesLinkingAfterProviderSuccess(t *testing.T) {
	appID := "22222222-2222-4222-8222-222222222222"
	issueID := "33333333-3333-4333-8333-333333333333"
	requestID := "11111111-1111-4111-8111-111111111111"
	writes, reads := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "GET" {
			reads++
			if r.URL.Path == "/v1/apps/payments" {
				_ = json.NewEncoder(w).Encode(api.AppResponse{ID: appID, Slug: "payments"})
				return
			}
			if r.URL.Path != "/v1/apps/payments/issues/"+issueID {
				t.Errorf("unexpected read: %s", r.URL)
			}
			_ = json.NewEncoder(w).Encode(api.IssueDetail{Issue: api.Issue{ID: issueID, AppID: appID}})
			return
		}
		writes++
		if r.Method != "POST" || r.URL.Path != "/v1/apps/payments/issues/"+issueID+"/tracker-links" {
			t.Errorf("unexpected link: %s", r.URL)
		}
		if writes == 1 {
			w.WriteHeader(503)
			_, _ = w.Write([]byte(`{"code":"capacity_unavailable"}`))
			return
		}
		var in api.PutIssueTrackerLinkRequest
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.URL != "https://github.com/owner/repo/issues/42" || in.Title != "Fix checkout" {
			t.Errorf("link payload: %+v %v", in, err)
		}
		_ = json.NewEncoder(w).Encode(api.IssueTrackerLink{ID: "55555555-5555-4555-8555-555555555555", URL: in.URL})
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	t.Setenv("GREGALE_GITHUB_TOKEN", "secret")
	previousOut, previousJSON := osStdout, jsonOutput
	var out bytes.Buffer
	osStdout, jsonOutput = &out, true
	t.Cleanup(func() { osStdout, jsonOutput = previousOut, previousJSON })
	provider := &ticketCLIProvider{}
	args := []string{"create", "--app", "payments", "--issue", issueID, "--provider", "github", "--target", "OWNER/Repo", "--title", "Fix checkout", "--request-id", requestID, "--journal-dir", t.TempDir()}
	if runIssueTickets(args, provider) == 0 {
		t.Fatal("failed linking reported success")
	}
	out.Reset()
	t.Setenv("GREGALE_GITHUB_TOKEN", "")
	if runIssueTickets(args, provider) != 0 {
		t.Fatal("retry did not resume linking")
	}
	var outcome issuetracker.Outcome
	if err := json.Unmarshal(out.Bytes(), &outcome); err != nil || outcome.State != "linked" || outcome.LinkID == "" {
		t.Fatalf("JSON outcome: %s %v", out.String(), err)
	}
	if provider.creates != 1 || writes != 2 || reads != 4 {
		t.Fatalf("duplicate creation or missing reads: %d %d %d", provider.creates, writes, reads)
	}
	if cmdIssues([]string{"tickets", "create", "--app", "payments", "--issue", issueID, "--provider", "github", "--target", "owner/repo", "--title", "Fix", "--request-id", "invalid"}) == 0 || reads != 4 {
		t.Fatal("invalid inputs reached the network")
	}
}

func TestIssueTicketNativeCLIUsesServerReceiptWithoutProviderCredential(t *testing.T) {
	appID := "22222222-2222-4222-8222-222222222222"
	issueID := "33333333-3333-4333-8333-333333333333"
	requestID := "11111111-1111-4111-8111-111111111111"
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "GET" {
			if r.URL.Path == "/v1/apps/payments" {
				_ = json.NewEncoder(w).Encode(api.AppResponse{ID: appID, Slug: "payments"})
				return
			}
			_ = json.NewEncoder(w).Encode(api.IssueDetail{Issue: api.Issue{ID: issueID, AppID: appID}})
			return
		}
		calls++
		if r.Method != "POST" || r.URL.Path != "/v1/apps/payments/issues/"+issueID+"/native-tickets" {
			t.Error("wrong native create route")
		}
		var in api.CreateNativeIssueTicketRequest
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.RequestID != requestID || in.Repository != "owner/repo" || in.Title != "Fix" {
			t.Error("native inputs lost", in, err)
		}
		state := "created"
		linkID := ""
		if calls > 1 {
			state = "linked"
			linkID = "55555555-5555-4555-8555-555555555555"
		}
		_ = json.NewEncoder(w).Encode(api.NativeIssueTicketResult{RequestID: requestID, State: state, TicketURL: "https://github.com/owner/repo/issues/42", LinkID: linkID})
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	t.Setenv("GREGALE_GITHUB_TOKEN", "")
	oldOut, oldJSON := osStdout, jsonOutput
	var out bytes.Buffer
	osStdout, jsonOutput = &out, true
	t.Cleanup(func() { osStdout, jsonOutput = oldOut, oldJSON })
	provider := &ticketCLIProvider{}
	args := []string{"create", "--app", "payments", "--issue", issueID, "--provider", "github", "--target", "OWNER/Repo", "--title", "Fix", "--request-id", requestID, "--native"}
	if runIssueTickets(args, provider) == 0 {
		t.Fatal("incomplete attachment reported success")
	}
	out.Reset()
	if runIssueTickets(args, provider) != 0 || provider.creates != 0 || calls != 2 {
		t.Fatal("native create used local provider or failed retry")
	}
	if runIssueTickets(append(args, "--token-env", "LOCAL_TOKEN"), provider) == 0 || calls != 2 {
		t.Fatal("native create allowed provider credential")
	}
}

func TestIssueTicketAutoSyncCLIRequiresAlignmentAndPreservesPolicyRevision(t *testing.T) {
	issueID := "33333333-3333-4333-8333-333333333333"
	linkID := "55555555-5555-4555-8555-555555555555"
	snapshot := api.NativeIssueTrackerStatus{IssueID: issueID, LinkID: linkID, InstallationID: 42, Repository: "owner/repo", RepositoryID: 100, Status: api.IssueTrackerProviderStatus{Provider: "github", URL: "https://github.com/owner/repo/issues/42", ID: "42", State: "open"}}
	policy := api.IssueTrackerAutoSync{LinkID: linkID, SourceIssueID: issueID, Outcome: "disabled"}
	writes := 0
	source := "open"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/apps/payments/issues/"+issueID {
			_ = json.NewEncoder(w).Encode(api.IssueDetail{Issue: api.Issue{ID: issueID, State: source}})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/native-status") {
			_ = json.NewEncoder(w).Encode(snapshot)
			return
		}
		if r.URL.Path != "/v1/apps/payments/issues/"+issueID+"/tracker-links/"+linkID+"/auto-sync" {
			t.Error("wrong policy route")
		}
		if r.Method == "PUT" {
			writes++
			var in api.UpdateIssueTrackerAutoSyncRequest
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.ExpectedRevision != policy.Revision {
				t.Error("policy revision lost", in, err)
			}
			if in.Enabled && (in.ExpectedStatus == nil || *in.ExpectedStatus != snapshot || in.ExpectedIssueState != "open") {
				t.Error("aligned snapshot lost")
			}
			policy.Enabled = in.Enabled
			policy.Revision++
			policy.Outcome = "disabled"
			if in.Enabled {
				policy.Outcome = "matched"
			}
		}
		_ = json.NewEncoder(w).Encode(policy)
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	t.Setenv("GREGALE_GITHUB_TOKEN", "")
	previousOut := osStdout
	var out bytes.Buffer
	osStdout = &out
	t.Cleanup(func() { osStdout = previousOut })
	args := []string{"auto-sync", "--app", "payments", "--issue", issueID, "--link", linkID}
	if cmdIssueTickets(append(args, "--enable")) != 0 || writes != 1 {
		t.Fatal("native policy enable failed")
	}
	if cmdIssueTickets(append(args, "--disable")) != 0 || writes != 2 {
		t.Fatal("native policy disable failed")
	}
	source = "resolved"
	if cmdIssueTickets(append(args, "--enable")) == 0 || writes != 2 {
		t.Fatal("unaligned policy enabled")
	}
	if cmdIssueTickets(append(args, "--enable", "--disable")) == 0 || writes != 2 {
		t.Fatal("ambiguous mode reached provider")
	}
}
