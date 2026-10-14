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
