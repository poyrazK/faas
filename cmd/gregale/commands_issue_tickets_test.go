package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
