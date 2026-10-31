package faas_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	faas "github.com/poyrazK/faas/sdk/go"
	"github.com/poyrazK/faas/sdk/go/preview"
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
	input := preview.CreateNativeIssueTicketRequest{RequestID: "11111111-1111-4111-8111-111111111111", Repository: "owner/repo", Title: "Fix", RecoveryURL: "https://github.com/owner/repo/issues/42"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/apps/payments/issues/issue/native-tickets" {
			t.Error("wrong creation route")
		}
		var in preview.CreateNativeIssueTicketRequest
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in != input {
			t.Error("creation identity lost", in, err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(preview.NativeIssueTicketResult{RequestID: in.RequestID, State: "linked", TicketURL: in.RecoveryURL, LinkID: "link"})
	}))
	defer server.Close()
	client, err := faas.NewClient(server.URL, "fp_live_test")
	if err != nil {
		t.Fatal(err)
	}
	out, err := preview.Wrap(client).CreateNativeIssueTicket(faas.SDKTestContext(t), "payments", "issue", input)
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
	if _, err := client.GetIssueTrackerAutoSync(faas.SDKTestContext(t), "payments", "issue", "link"); err != nil {
		t.Fatal(err)
	}
	out, err := client.UpdateIssueTrackerAutoSync(faas.SDKTestContext(t), "payments", "issue", "link", input)
	if err != nil || !out.Enabled || out.Revision != 8 {
		t.Fatal(out, err)
	}
}

func TestIssueNativeLinearConnectionSDKPreservesDecisionAndRevision(t *testing.T) {
	input := preview.UpdateIssueTrackerLinearConnectionRequest{Connected: true, ExpectedRevision: 7, TeamID: "team", APIKey: "secret"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/apps/payments/issue-tracker-linear-connection" {
			t.Error("wrong connection route")
		}
		if r.Method == "PUT" {
			var in preview.UpdateIssueTrackerLinearConnectionRequest
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in != input {
				t.Error("connection input lost", err)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(preview.IssueTrackerLinearConnection{Connected: true, Revision: 8, TeamID: "team", AuthMethod: "oauth", OAuthStatus: "reconnect_required"})
	}))
	defer server.Close()
	client, err := faas.NewClient(server.URL, "fp_live_test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := preview.Wrap(client).GetIssueTrackerLinearConnection(faas.SDKTestContext(t), "payments"); err != nil {
		t.Fatal(err)
	}
	out, err := preview.Wrap(client).UpdateIssueTrackerLinearConnection(faas.SDKTestContext(t), "payments", input)
	if err != nil || out.Revision != 8 || !out.Connected || out.AuthMethod != "oauth" || out.OAuthStatus != "reconnect_required" {
		t.Fatal("connection response lost", err)
	}
}

func TestIssueNativeLinearCreationSDKPreservesUUIDRecovery(t *testing.T) {
	input := preview.CreateNativeLinearIssueTicketRequest{RequestID: "11111111-1111-4111-8111-111111111111", TeamID: "22222222-2222-4222-8222-222222222222", Title: "Fix", Recover: true}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/apps/payments/issues/issue/native-linear-tickets" {
			t.Error("wrong Linear creation route")
		}
		var in preview.CreateNativeLinearIssueTicketRequest
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in != input {
			t.Error("UUID recovery lost", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(preview.NativeIssueTicketResult{RequestID: in.RequestID, State: "linked", TicketURL: "https://linear.app/acme/issue/PAY-42", LinkID: "link"})
	}))
	defer server.Close()
	client, err := faas.NewClient(server.URL, "fp_live_test")
	if err != nil {
		t.Fatal(err)
	}
	out, err := preview.Wrap(client).CreateNativeLinearIssueTicket(faas.SDKTestContext(t), "payments", "issue", input)
	if err != nil || out.RequestID != input.RequestID || out.State != "linked" {
		t.Fatal(out, err)
	}
}

func TestIssueNativeLinearStatusSDKPreservesPreviewFences(t *testing.T) {
	snapshot := faas.NativeLinearIssueTrackerStatus{IssueID: "issue", LinkID: "link", ConnectionRevision: 7, GroupFingerprint: "group", WorkspaceID: "workspace", TeamID: "team", ViewerID: "viewer", Status: faas.IssueTrackerProviderStatus{Provider: "linear", URL: "https://linear.app/acme/issue/PAY-42", ID: "ticket", TeamID: "team", StateID: "state", State: "resolved"}}
	input := faas.UpdateNativeLinearIssueTrackerStatusRequest{DesiredState: "open", Expected: snapshot.Status, ExpectedConnectionRevision: 7, ExpectedGroupFingerprint: "group", LinearStateID: "workflow"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/apps/payments/issues/issue/tracker-links/link/native-linear-status" {
			t.Error("wrong Linear status path")
		}
		if r.Method == "POST" {
			var in faas.UpdateNativeLinearIssueTrackerStatusRequest
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in != input {
				t.Error("preview fences lost", err)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(snapshot)
	}))
	defer server.Close()
	client, err := faas.NewClient(server.URL, "fp_live_test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetNativeLinearIssueTrackerStatus(faas.SDKTestContext(t), "payments", "issue", "link"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.UpdateNativeLinearIssueTrackerStatus(faas.SDKTestContext(t), "payments", "issue", "link", input); err != nil {
		t.Fatal(err)
	}
}

func TestIssueNativeLinearAutoSyncSDKPreservesMappingsAndScope(t *testing.T) {
	snapshot := faas.NativeLinearIssueTrackerStatus{IssueID: "issue", LinkID: "link", ConnectionRevision: 7, GroupFingerprint: "group", Status: faas.IssueTrackerProviderStatus{Provider: "linear", State: "open"}}
	input := faas.UpdateIssueTrackerAutoSyncRequest{Direction: "both", Enabled: true, ExpectedRevision: 3, ExpectedLinearStatus: &snapshot, ExpectedIssueState: "open", LinearOpenStateID: "open", LinearResolvedStateID: "resolved"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" || r.URL.Path != "/v1/apps/payments/issues/issue/tracker-links/link/auto-sync" {
			t.Error("wrong operation")
		}
		var in faas.UpdateIssueTrackerAutoSyncRequest
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.ExpectedLinearStatus == nil || *in.ExpectedLinearStatus != snapshot || in.LinearOpenStateID != "open" || in.LinearResolvedStateID != "resolved" || in.ExpectedRevision != 3 || in.Direction != "both" {
			t.Error("mapping or scope lost", err)
		}
		_ = json.NewEncoder(w).Encode(faas.IssueTrackerAutoSync{Direction: "both", ObservedState: "resolved", LinkID: "link", SourceIssueID: "issue", Enabled: true, Revision: 4, Outcome: "matched", LinearOpenStateID: "open", LinearResolvedStateID: "resolved"})
	}))
	defer server.Close()
	client, err := faas.NewClient(server.URL, "fp_live_test")
	if err != nil {
		t.Fatal(err)
	}
	out, err := client.UpdateIssueTrackerAutoSync(faas.SDKTestContext(t), "payments", "issue", "link", input)
	if err != nil || out.LinearOpenStateID != "open" || out.LinearResolvedStateID != "resolved" || out.Direction != "both" || out.ObservedState != "resolved" {
		t.Fatal(out, err)
	}
}

func TestIssueNativeLinearCallbackSDKPreservesWriteOnlySecretAndMetadata(t *testing.T) {
	secret := "linear_signing_secret_1234567890abcdef"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in preview.UpdateIssueTrackerLinearConnectionRequest
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.WebhookSecret != secret {
			t.Error("signing secret lost", err)
		}
		_ = json.NewEncoder(w).Encode(preview.IssueTrackerLinearConnection{Connected: true, Revision: 3, WebhookEnabled: true, CallbackPath: "/hooks/issue-trackers/linear/app"})
	}))
	defer server.Close()
	client, err := faas.NewClient(server.URL, "fp_live_test")
	if err != nil {
		t.Fatal(err)
	}
	out, err := preview.Wrap(client).UpdateIssueTrackerLinearConnection(faas.SDKTestContext(t), "payments", preview.UpdateIssueTrackerLinearConnectionRequest{Connected: true, ExpectedRevision: 2, APIKey: "key", TeamID: "team", WebhookSecret: secret})
	if err != nil || !out.WebhookEnabled || out.CallbackPath != "/hooks/issue-trackers/linear/app" {
		t.Fatal(out, err)
	}
}
