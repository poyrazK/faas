package faas_test

import (
	"context"
	"encoding/json"
	faas "github.com/poyrazK/faas/sdk/go"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIssueNativeTrackerSDKKeepsExpectedSnapshot(t *testing.T) {
	snapshot := faas.IssueTrackerProviderStatus{Provider: "github", URL: "https://github.com/owner/repo/issues/42", ID: "42", State: "resolved"}
	response := faas.NativeIssueTrackerStatus{IssueID: "issue", LinkID: "link", InstallationID: 42, Repository: "owner/repo", RepositoryID: 100, Status: snapshot}
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/v1/apps/payments/issues/issue/tracker-links/link/native-status" {
			t.Error("native route changed")
		}
		if r.Method == "POST" {
			writes++
			var request faas.UpdateNativeIssueTrackerStatusRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Expected != snapshot || request.DesiredState != "open" {
				t.Error("expected snapshot dropped", err)
			}
			response.Status.State = "open"
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()
	client, err := faas.NewClient(server.URL, "fp_live_test")
	if err != nil {
		t.Fatal(err)
	}
	observed, err := client.GetNativeIssueTrackerStatus(context.Background(), "payments", "issue", "link")
	if err != nil || observed.RepositoryID != 100 {
		t.Fatal("native read failed", err)
	}
	final, err := client.UpdateNativeIssueTrackerStatus(context.Background(), "payments", "issue", "link", faas.UpdateNativeIssueTrackerStatusRequest{DesiredState: "open", Expected: observed.Status})
	if err != nil || final.Status.State != "open" || writes != 1 {
		t.Fatal("native update failed", err)
	}
}

func TestIssueNativeCreationSDKRetainsRequestIdentity(t *testing.T) {
	input := faas.CreateNativeIssueTicketRequest{RequestID: "11111111-1111-4111-8111-111111111111", Repository: "owner/repo", Title: "Fix", RecoveryURL: "https://github.com/owner/repo/issues/42"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/apps/payments/issues/issue/native-tickets" {
			t.Error("wrong creation route")
		}
		var in faas.CreateNativeIssueTicketRequest
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in != input {
			t.Error("creation identity lost", in, err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(faas.NativeIssueTicketResult{RequestID: in.RequestID, State: "linked", TicketURL: in.RecoveryURL, LinkID: "link"})
	}))
	defer server.Close()
	client, err := faas.NewClient(server.URL, "fp_live_test")
	if err != nil {
		t.Fatal(err)
	}
	out, err := client.CreateNativeIssueTicket(t.Context(), "payments", "issue", input)
	if err != nil || out.RequestID != input.RequestID || out.State != "linked" {
		t.Fatal(out, err)
	}
}

func TestIssueNativeAutoSyncSDKKeepsRevisionAndDelegationSnapshot(t *testing.T) {
	snapshot := faas.NativeIssueTrackerStatus{IssueID: "issue", LinkID: "link", Repository: "owner/repo", RepositoryID: 100, InstallationID: 42, Status: faas.IssueTrackerProviderStatus{Provider: "github", URL: "https://github.com/owner/repo/issues/42", ID: "42", State: "open"}}
	input := faas.UpdateIssueTrackerAutoSyncRequest{Enabled: true, ExpectedRevision: 7, ExpectedStatus: &snapshot, ExpectedIssueState: "open"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/v1/apps/payments/issues/issue/tracker-links/link/auto-sync" {
			t.Error("wrong policy route")
		}
		if r.Method == "PUT" {
			var in faas.UpdateIssueTrackerAutoSyncRequest
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil || !in.Enabled || in.ExpectedRevision != 7 || in.ExpectedStatus == nil || *in.ExpectedStatus != snapshot || in.ExpectedIssueState != "open" {
				t.Error("delegation snapshot lost", err)
			}
		}
		_ = json.NewEncoder(w).Encode(faas.IssueTrackerAutoSync{LinkID: "link", Enabled: r.Method == "PUT", Revision: 8, SourceIssueID: "issue", Outcome: "matched"})
	}))
	defer server.Close()
	client, err := faas.NewClient(server.URL, "fp_live_test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetIssueTrackerAutoSync(t.Context(), "payments", "issue", "link"); err != nil {
		t.Fatal(err)
	}
	out, err := client.UpdateIssueTrackerAutoSync(t.Context(), "payments", "issue", "link", input)
	if err != nil || !out.Enabled || out.Revision != 8 {
		t.Fatal(out, err)
	}
}
