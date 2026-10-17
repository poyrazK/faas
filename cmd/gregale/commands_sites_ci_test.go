package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hosting"
)

func TestHostingCIRetriesReuseRunReleaseAndPreviewAfterLostResponses(t *testing.T) {
	for _, reserve := range []bool{false, true} {
		t.Run(fmt.Sprint(reserve), func(t *testing.T) {
			// adr: 202610170925 — neither shared replay receipts nor successful responses allocate identities.
			site := reviewSiteID
			id := reviewReleaseID
			releaseID := "33333333-3333-4333-8333-333333333333"
			previewID := "44444444-4444-4444-8444-444444444444"
			now := time.Now().UTC()
			req := api.HostingCIRunRequest{Manifest: api.HostingManifest{Files: []api.HostingFile{{Path: "index.html", Size: 1, SHA256: strings.Repeat("a", 64), ContentType: "text/html"}}}, PreviewTTLSeconds: 3600, ReservePreview: reserve}
			fingerprint, _ := hosting.CIFingerprint(req)
			run := api.HostingCIRun{ID: id, SiteID: site, ReleaseID: releaseID, PreviewID: previewID, Fingerprint: fingerprint, PreviewReserved: reserve, CreatedAt: now, PreviewExpiresAt: now.Add(time.Hour)}
			created, previewCreated, puts, previewCalls := false, false, 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer site-token" {
					t.Error("missing scoped auth")
				}
				w.Header().Set("Content-Type", "application/json")
				root := "/v1/internal/hosting/sites/" + site
				switch r.Method + " " + r.URL.Path {
				case "GET /v1/internal/hosting/capabilities":
					_ = json.NewEncoder(w).Encode(api.HostingCapabilities{AutomationEnabled: true, AutomationSiteID: site, AutomationOperations: []string{"stage", "preview"}, StagedReleasesSupported: true, PreviewsEnabled: true})
				case "GET " + root:
					_ = json.NewEncoder(w).Encode(api.HostingSite{ID: site, Hostname: site + ".hosting.example", Status: "active", DurableRouting: true})
				case "PUT " + root + "/ci/" + id:
					puts++
					if !created {
						created = true
						w.WriteHeader(503)
						_ = json.NewEncoder(w).Encode(api.NewProblem(503, "interrupted", "Lost response", ""))
						return
					}
					run.PreviewCreated = previewCreated
					_ = json.NewEncoder(w).Encode(run)
				case "GET " + root + "/releases":
					_ = json.NewEncoder(w).Encode([]api.HostingRelease{{ID: releaseID, SiteID: site, State: "ready", Manifest: req.Manifest}})
				case "POST " + root + "/ci/" + id + "/preview":
					previewCalls++
					if !previewCreated {
						previewCreated = true
						w.WriteHeader(503)
						_ = json.NewEncoder(w).Encode(api.NewProblem(503, "interrupted", "Lost response", ""))
						return
					}
					_ = json.NewEncoder(w).Encode(api.HostingPreview{ID: previewID, SiteID: site, ReleaseID: releaseID, Hostname: "p-" + previewID + ".hosting.example", Generation: 1, PublishedGeneration: 1, PublishedAt: &now, ExpiresAt: run.PreviewExpiresAt})
				case "GET " + root + "/status":
					_ = json.NewEncoder(w).Encode(api.HostingSiteStatus{Site: api.HostingSite{ID: site, Status: "active", DurableRouting: true}, Publication: api.HostingPublicationStatus{State: "published", Generation: 2, PublishedGeneration: 2, PublishedAt: &now}})
				default:
					t.Error("unexpected authority or network call", r.Method, r.URL.Path)
					w.WriteHeader(403)
				}
			}))
			defer server.Close()
			client := api.NewHostingAutomationClient(server.URL, "site-token")
			first, err := runHostingCI(t.Context(), client, site, id, "unused", req, time.Second)
			if err == nil || first.Run.ID != id {
				t.Fatal(first, err)
			}
			second, err := runHostingCI(t.Context(), client, site, id, "unused", req, time.Second)
			if err == nil || second.Run.ReleaseID != releaseID {
				t.Fatal(second, err)
			}
			third, err := runHostingCI(t.Context(), client, site, id, "unused", req, time.Second)
			if err != nil || !third.Published || third.State != "publication_acknowledged" || third.Run.ReleaseID != releaseID || puts != 3 || previewCalls != 2 {
				t.Fatal(third, puts, previewCalls, err)
			}
		})
	}
}
func TestSitesCIRefusesBroadCredentialsAndInvalidIntentBeforeNetworking(t *testing.T) {
	calls := 0
	reviewCLI(t, func(http.ResponseWriter, *http.Request) { calls++ })
	t.Setenv("FAAS_FRONTEND_HOSTING_AUTOMATION_ENABLED", "1")
	for _, args := range [][]string{{"ci", "--site", reviewSiteID, "--run-id", reviewReleaseID}, {"ci", "--site", reviewSiteID, "--run-id", "bad"}, {"ci", "--site", reviewSiteID, "--run-id", reviewReleaseID, "--wait-timeout", "20m"}} {
		if cmdSites(args) == 0 {
			t.Fatal("invalid CI options accepted")
		}
	}
	if calls != 0 {
		t.Fatal("invalid credential reached network")
	}
}
func TestSitesTokenCreationWritesPrivateFileWithoutPrintingSecret(t *testing.T) {
	var stdout bytes.Buffer
	old := osStdout
	osStdout = &stdout
	defer func() { osStdout = old }()
	token := "fp_host_" + strings.Repeat("a", 64)
	calls := 0
	reviewCLI(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.Header.Get("Idempotency-Key") != "" {
			t.Error("one-time credential was cacheable")
		}
		_ = json.NewEncoder(w).Encode(api.HostingAutomationCredentialResponse{Credential: api.HostingAutomationCredential{ID: reviewReleaseID, SiteID: reviewSiteID}, Token: token})
	})
	t.Setenv("FAAS_FRONTEND_HOSTING_AUTOMATION_ENABLED", "1")
	path := filepath.Join(t.TempDir(), "ci-token")
	args := []string{"tokens", "create", "--site", reviewSiteID, "--label", "CI", "--out", path}
	if cmdSites(args) != 0 {
		t.Fatal("issuance failed")
	}
	contents, err := os.ReadFile(path)
	if err != nil || string(contents) != token+"\n" {
		t.Fatal("private credential file missing", err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 || strings.Contains(stdout.String(), token) {
		t.Fatal("credential exposed")
	}
	if cmdSites(args) == 0 || calls != 1 {
		t.Fatal("existing private file overwritten")
	}
}
func TestCIUploadGrantsCannotEscapeTheirReleaseOrCarryCredentials(t *testing.T) {
	run := api.HostingCIRun{SiteID: reviewSiteID, ReleaseID: reviewReleaseID}
	manifest := api.HostingManifest{Files: []api.HostingFile{{Path: "index.html", Size: 1, ContentType: "text/html"}}}
	valid := "https://private.r2.example/bucket/hosting/staging/" + run.SiteID + "/" + run.ReleaseID + "/index.html?signature=test"
	for _, url := range []string{"https://other.example/collect", strings.Replace(valid, run.ReleaseID, run.SiteID, 1)} {
		response := api.HostingUploadResponse{Release: api.HostingRelease{ID: run.ReleaseID, SiteID: run.SiteID, State: "pending"}, Uploads: []api.HostingUpload{{Path: "index.html", URL: url}}}
		if validateCIUploadGrants(response, run, manifest, "https://private.r2.example/bucket") == nil {
			t.Fatal("foreign upload origin/release allowed")
		}
	}
	response := api.HostingUploadResponse{Release: api.HostingRelease{ID: run.ReleaseID, SiteID: run.SiteID, State: "pending"}, Uploads: []api.HostingUpload{{Path: "index.html", URL: valid, Headers: map[string][]string{"Authorization": {"Bearer forbidden"}}}}}
	if validateCIUploadGrants(response, run, manifest, "https://private.r2.example/bucket") == nil {
		t.Fatal("credential-bearing upload grant allowed")
	}
}

func TestHostingCIExpiredIntentStopsBeforeUploadsOrPreviewMutation(t *testing.T) {
	// adr: 202610171001 — retained but expired CI identity cannot restart cleanup-bound work.
	req := api.HostingCIRunRequest{Manifest: api.HostingManifest{Files: []api.HostingFile{{Path: "index.html", SHA256: strings.Repeat("a", 64), Size: 1, ContentType: "text/html"}}}, PreviewTTLSeconds: 3600}
	fingerprint, _ := hosting.CIFingerprint(req)
	run := api.HostingCIRun{ID: reviewReleaseID, SiteID: reviewSiteID, ReleaseID: "33333333-3333-4333-8333-333333333333", PreviewID: "44444444-4444-4444-8444-444444444444", Fingerprint: fingerprint, PreviewExpiresAt: time.Now().Add(-time.Hour)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/capabilities"):
			_ = json.NewEncoder(w).Encode(api.HostingCapabilities{AutomationEnabled: true, AutomationSiteID: reviewSiteID, AutomationOperations: []string{"stage", "preview"}, StagedReleasesSupported: true, PreviewsEnabled: true})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, reviewSiteID):
			_ = json.NewEncoder(w).Encode(api.HostingSite{ID: reviewSiteID, DurableRouting: true, Status: "active"})
		case r.Method == "PUT" && strings.Contains(r.URL.Path, "/ci/"):
			_ = json.NewEncoder(w).Encode(run)
		default:
			t.Error("expired run attempted more work", r.Method, r.URL.Path)
			w.WriteHeader(403)
		}
	}))
	defer server.Close()
	result, err := runHostingCI(t.Context(), api.NewHostingAutomationClient(server.URL, "scoped-test-token"), reviewSiteID, run.ID, "unused", req, time.Second)
	if err == nil || result.Published || result.Run.ReleaseID != run.ReleaseID {
		t.Fatal(result, err)
	}
}

func TestSitesCIOIDCRequiresExplicitFlagAndNeverFallsBackToSavedCredentials(t *testing.T) {
	t.Setenv("FAAS_FRONTEND_HOSTING_ENABLED", "1")
	t.Setenv("FAAS_FRONTEND_HOSTING_AUTOMATION_ENABLED", "1")
	t.Setenv("FAAS_API", "https://api.example.com")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "https://runner.actions.githubusercontent.com/job")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "test-job-identity")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("static"), 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"--site", reviewSiteID, "--run-id", reviewReleaseID, "--oidc-trust", reviewReleaseID, "--path", dir}
	for _, settings := range []struct{ flag, token string }{{"", ""}, {"false", ""}, {"1", "fp_live_account_token"}, {"1", "fp_host_" + strings.Repeat("a", 64)}} {
		t.Setenv("FAAS_FRONTEND_HOSTING_OIDC_ENABLED", settings.flag)
		t.Setenv("FAAS_TOKEN", settings.token)
		if code := cmdSitesCI(args); code == 0 {
			t.Fatal("OIDC fallback or disabled flow succeeded")
		}
	}
	t.Setenv("FAAS_TOKEN", "")
	t.Setenv("FAAS_FRONTEND_HOSTING_OIDC_ENABLED", "1")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "https://attacker.example/job")
	if code := cmdSitesCI(args); code == 0 {
		t.Fatal("GitHub identity request credential sent to untrusted endpoint")
	}
}
