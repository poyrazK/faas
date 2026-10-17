package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestStaticHostingManifestRejectsLinks(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>ok</html>"), 0600); err != nil {
		t.Fatal(err)
	}
	manifest, err := staticHostingManifest(dir, true)
	if err != nil || len(manifest.Files) != 1 || !manifest.SPAFallback {
		t.Fatalf("manifest %+v %v", manifest, err)
	}
	if err := os.Symlink("index.html", filepath.Join(dir, "linked.html")); err != nil {
		t.Fatal(err)
	}
	if _, err := staticHostingManifest(dir, false); err == nil {
		t.Fatal("accepted symlink")
	}
}

func TestHostingUploadDoesNotForwardPlatformCredentialsOrRedirect(t *testing.T) {
	var redirected bool
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected = true; w.WriteHeader(200) }))
	defer target.Close()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("platform credentials reached object storage")
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != "site" || r.ContentLength != 4 {
			t.Errorf("upload body %q, length %d", body, r.ContentLength)
		}
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "index.html")
	if err := os.WriteFile(path, []byte("site"), 0600); err != nil {
		t.Fatal(err)
	}
	client := server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	err := uploadHostingFile(context.Background(), client, path, api.HostingFile{Path: "index.html", Size: 4, ContentType: "text/html"}, api.HostingUpload{URL: server.URL + "?secret=must-not-print"})
	if err == nil || redirected || strings.Contains(err.Error(), "must-not-print") {
		t.Fatalf("redirect or secret exposure: %v", err)
	}
}

func TestSitesCommandDefaultOff(t *testing.T) {
	t.Setenv("FAAS_FRONTEND_HOSTING_ENABLED", "")
	if status := cmdSites([]string{"list"}); status == 0 {
		t.Fatal("internal CLI enabled by default")
	}
}

func TestSitesStatusUsesAuthenticatedReadEndpoint(t *testing.T) {
	t.Setenv("FAAS_FRONTEND_HOSTING_ENABLED", "1")
	t.Setenv("FAAS_TOKEN", "test-token")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodGet || r.URL.Path != "/v1/internal/hosting/sites/11111111-1111-4111-8111-111111111111/status" || r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("incorrect status request: %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(api.HostingSiteStatus{Publication: api.HostingPublicationStatus{State: "pending", Generation: 3, PublishedGeneration: 2}})
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	if code := cmdSites([]string{"status", "--site", "11111111-1111-4111-8111-111111111111"}); code != 0 || calls != 1 {
		t.Fatalf("status command=%d calls=%d", code, calls)
	}
}

func TestSitesSuspensionUsesCurrentRevision(t *testing.T) {
	for _, verb := range []string{"suspend", "resume"} {
		t.Run(verb, func(t *testing.T) {
			t.Setenv("FAAS_FRONTEND_HOSTING_ENABLED", "1")
			t.Setenv("FAAS_TOKEN", "test-token")
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Header.Get("Authorization") != "Bearer test-token" {
					t.Error("missing operator authentication")
				}
				if r.Method == http.MethodGet {
					_ = json.NewEncoder(w).Encode(api.HostingSite{ID: "11111111-1111-4111-8111-111111111111", Revision: 9, DurableRouting: true})
					return
				}
				if r.Method != http.MethodPut || !strings.HasSuffix(r.URL.Path, "/suspension") {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				var req api.SetHostingSuspendedRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
				}
				if req.ExpectedRevision != 9 || req.Suspended != (verb == "suspend") {
					t.Errorf("incorrect suspension request: %+v", req)
				}
				w.WriteHeader(202)
				_ = json.NewEncoder(w).Encode(api.HostingSite{Revision: 10, Suspended: req.Suspended})
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			if code := cmdSites([]string{verb, "--site", "11111111-1111-4111-8111-111111111111"}); code != 0 || calls != 2 {
				t.Fatalf("command=%d calls=%d", code, calls)
			}
		})
	}
}

func TestStaticHostingReleaseConfiguration(t *testing.T) {
	// adr: 202610161909 — release policies are bounded and independently gated.
	dir := t.TempDir()
	for path, body := range map[string]string{"index.html": "home", "404.html": "missing", "gregale-hosting.json": `{"version":1,"not_found":"404.html","redirects":[{"from":"/old","to":"/new","status":308}]}`} {
		if err := os.WriteFile(filepath.Join(dir, path), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("FAAS_FRONTEND_HOSTING_CONFIG_ENABLED", "")
	if _, err := staticHostingManifest(dir, false); err == nil {
		t.Fatal("configuration enabled by default")
	}
	t.Setenv("FAAS_FRONTEND_HOSTING_CONFIG_ENABLED", "1")
	m, err := staticHostingManifest(dir, false)
	if err != nil || m.Config == nil || m.Config.NotFound != "404.html" || len(m.Files) != 2 {
		t.Fatalf("manifest %+v %v", m, err)
	}
	for _, f := range m.Files {
		if f.Path == "gregale-hosting.json" {
			t.Fatal("configuration published as file")
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "gregale-hosting.json"), []byte(`{"version":1,"not_found":"missing.html"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := staticHostingManifest(dir, false); err == nil {
		t.Fatal("accepted missing custom 404")
	}
	if err := os.Remove(filepath.Join(dir, "gregale-hosting.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("index.html", filepath.Join(dir, "gregale-hosting.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := staticHostingManifest(dir, false); err == nil {
		t.Fatal("accepted symlink config")
	}
}
