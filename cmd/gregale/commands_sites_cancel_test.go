package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hosting"
)

func TestSitesCancelIsExplicitOperatorMutation(t *testing.T) {
	// adr: 202610171242 — exact-run cancellation is explicit and never automatically retried.
	calls := 0
	bad := false
	reviewCLI(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.URL.Path != "/v1/internal/hosting/sites/"+reviewSiteID+"/ci/"+reviewReleaseID+"/cancel" || r.Header.Get("Idempotency-Key") != "" {
			t.Error("unexpected cancellation", r.Method, r.URL.Path)
		}
		run := api.HostingCIRun{ID: reviewReleaseID, SiteID: reviewSiteID, ReleaseID: reviewReleaseID, PreviewID: reviewReleaseID, Canceled: true}
		if bad {
			run.Canceled = false
		}
		_ = json.NewEncoder(w).Encode(run)
	})
	var out bytes.Buffer
	old := (*main.OsStdout)
	(*main.OsStdout) = &out
	t.Cleanup(func() { (*main.OsStdout) = old })
	args := []string{"cancel", "--site", reviewSiteID, "--run-id", reviewReleaseID}
	t.Setenv("FAAS_FRONTEND_HOSTING_CANCELLATION_ENABLED", "")
	if main.CmdSites(args) == 0 || calls != 0 {
		t.Fatal("default off")
	}
	t.Setenv("FAAS_FRONTEND_HOSTING_CANCELLATION_ENABLED", "1")
	if main.CmdSites([]string{"cancel", "--site", reviewSiteID, "--run-id", "bad"}) == 0 || calls != 0 {
		t.Fatal("invalid ID sent")
	}
	if main.CmdSites(args) != 0 || calls != 1 {
		t.Fatal(out.String(), calls)
	}
	out.Reset()
	bad = true
	if main.CmdSites(args) == 0 || out.Len() != 0 || calls != 2 {
		t.Fatal("unconfirmed cancellation exposed", out.String(), calls)
	}
}
func TestHostingCICanceledIntentStopsBeforeUploads(t *testing.T) {
	// adr: 202610171242 — a late CI retry sees its original IDs and cannot resume work.
	req := api.HostingCIRunRequest{PreviewTTLSeconds: 3600}
	fingerprint, _ := hosting.CIFingerprint(req)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/internal/hosting/capabilities":
			_ = json.NewEncoder(w).Encode(api.HostingCapabilities{AutomationEnabled: true, AutomationSiteID: reviewSiteID, AutomationOperations: []string{"stage", "preview"}, StagedReleasesSupported: true, PreviewsEnabled: true})
		case "GET /v1/internal/hosting/sites/" + reviewSiteID:
			_ = json.NewEncoder(w).Encode(api.HostingSite{ID: reviewSiteID, Status: "active", DurableRouting: true})
		case "PUT /v1/internal/hosting/sites/" + reviewSiteID + "/ci/" + reviewReleaseID:
			_ = json.NewEncoder(w).Encode(api.HostingCIRun{ID: reviewReleaseID, SiteID: reviewSiteID, ReleaseID: reviewReleaseID, PreviewID: reviewReleaseID, Canceled: true, Fingerprint: fingerprint})
		default:
			t.Error("canceled intent continued", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	result, err := main.RunHostingCI(t.Context(), api.NewHostingAutomationClient(server.URL, "scoped-test-token"), reviewSiteID, reviewReleaseID, "unused", req, time.Second)
	if err == nil || result.State != "canceled" || !result.Run.Canceled || calls != 3 {
		t.Fatal(result, err, calls)
	}
}
