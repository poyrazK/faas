// adr: 20261101160300
package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// githubAPI is a fake apid for the github commands. status is the
// connection GET /v1/apps/api/github returns; projectID is the app's.
type githubAPI struct {
	mu        sync.Mutex
	status    string
	projectID string
	reposPath []string
	binds     []api.InstallBindRequest
	patches   []map[string]any
}

func (f *githubAPI) serve(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/api/github":
			_, _ = w.Write([]byte(f.status))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/github/repos":
			f.reposPath = append(f.reposPath, r.URL.RequestURI())
			_, _ = w.Write([]byte(`[{"full_name":"acme/api","default_branch":"trunk"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/api/github/deployment-policy":
			_ = json.NewEncoder(w).Encode(map[string]any{"project_id": f.projectID, "production_trigger": "webhook"})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/api/github/bind":
			var req api.InstallBindRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			f.binds = append(f.binds, req)
			_ = json.NewEncoder(w).Encode(map[string]any{"binding_id": "bind-1", "repo_full_name": req.RepoFullName, "production_branch": req.ProductionBranch})
		case r.Method == http.MethodPatch && r.URL.Path == "/v1/apps/api/github/deployment-policy":
			patch := map[string]any{}
			_ = json.NewDecoder(r.Body).Decode(&patch)
			f.patches = append(f.patches, patch)
			_ = json.NewEncoder(w).Encode(map[string]any{"project_id": f.projectID, "production_trigger": "actions"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	t.Setenv("HOME", t.TempDir())
}

const severalInstallations = `{"state":"installed","health":"unknown","connected":false,"installations":[{"installation_id":42,"github_login":"alice"},{"installation_id":43,"github_login":"acme"}]}`

// With several connected installations, a bind used to take whichever
// token had been re-sealed last.
func TestGithubBindAsksForInstallationWhenSeveral(t *testing.T) {
	fake := &githubAPI{status: severalInstallations}
	fake.serve(t)
	stderr, restore := main.CaptureStderr(t)
	code := main.CmdGithub([]string{"bind", "api", "--repo", "acme/api"})
	restore()
	if code == 0 || !strings.Contains(stderr.String(), "--installation-id") || !strings.Contains(stderr.String(), "43 (acme)") {
		t.Fatalf("exit = %d, stderr = %s; want a request for --installation-id naming the installations", code, stderr.String())
	}
	if len(fake.binds) != 0 {
		t.Fatalf("bound %v without an installation", fake.binds)
	}

	if code := main.CmdGithub([]string{"bind", "api", "--repo", "acme/api", "--installation-id", "43"}); code != 0 {
		t.Fatalf("bind with --installation-id exit = %d", code)
	}
	if len(fake.binds) != 1 || fake.binds[0].InstallationID != 43 || fake.binds[0].ProductionBranch != "" {
		t.Fatalf("bind = %+v, want installation 43 and the repository's default branch", fake.binds)
	}
}

func TestGithubReposListsTheAppsInstallation(t *testing.T) {
	fake := &githubAPI{status: `{"state":"bound","health":"healthy","connected":true,"installation_id":43,"repo_full_name":"acme/api"}`}
	fake.serve(t)
	_, restore := main.CaptureStdout(t)
	code := main.CmdGithub([]string{"repos", "api"})
	restore()
	if code != 0 || len(fake.reposPath) != 1 || fake.reposPath[0] != "/v1/github/repos?installation_id=43" {
		t.Fatalf("exit = %d, requests = %v, want the bound installation", code, fake.reposPath)
	}
}

func githubSetupRepo(t *testing.T) string {
	t.Helper()
	repoDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(repoDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repoDir)
	return repoDir
}

// `gregale github setup` on an app without a project failed with
// project_required after it had bound the app, and could not turn its
// previews off.
func TestGithubSetupForAppWithoutProject(t *testing.T) {
	repoDir := githubSetupRepo(t)
	fake := &githubAPI{status: `{"state":"installed","health":"healthy","connected":false,"installation_id":42}`}
	fake.serve(t)

	stderr, restore := main.CaptureStderr(t)
	code := main.CmdGithubSetup([]string{"api", "--repo", "acme/api", "--preview-ttl-hours", "24"})
	restore()
	if code == 0 || !strings.Contains(stderr.String(), "--preview-ttl-hours needs a project-backed app") || len(fake.binds) != 0 {
		t.Fatalf("exit = %d binds = %v stderr = %s; a project setting must be refused before any change", code, fake.binds, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(repoDir, ".github", "workflows")); err == nil {
		t.Fatal("setup wrote a workflow before refusing a project setting")
	}

	_, restoreOut := main.CaptureStdout(t)
	code = main.CmdGithubSetup([]string{"api", "--repo", "acme/api", "--no-preview"})
	restoreOut()
	if code != 0 {
		t.Fatalf("setup exit = %d", code)
	}
	if len(fake.binds) != 1 || fake.binds[0].ProductionBranch != "trunk" || fake.binds[0].InstallationID != 42 {
		t.Fatalf("bind = %+v, want installation 42 on the repository's default branch", fake.binds)
	}
	if len(fake.patches) != 1 || len(fake.patches[0]) != 2 || fake.patches[0]["production_trigger"] != "actions" || fake.patches[0]["preview_enabled"] != false {
		t.Fatalf("policy patches = %v, want production_trigger and preview_enabled only", fake.patches)
	}
	workflow, err := os.ReadFile(filepath.Join(repoDir, ".github", "workflows", "gregale.yml"))
	if err != nil || !strings.Contains(string(workflow), `- "trunk"`) {
		t.Fatalf("workflow = %s, %v; want the trunk branch", workflow, err)
	}
}
