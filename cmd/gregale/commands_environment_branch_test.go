// adr: 20261031193635
package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func environmentBranchVisible() bool {
	for _, command := range main.VisibleCliCommands() {
		if command.Name != "projects" {
			continue
		}
		for _, sub := range command.Subcommands {
			if sub.Name != "environments" {
				continue
			}
			for _, verb := range sub.Subcommands {
				if verb.Name == "branch" {
					return true
				}
			}
		}
	}
	return false
}

// Branches are dark: the CLI hides `environment branch` from discovery and
// dispatch until FAAS_ENVIRONMENT_BRANCHES_ENABLED is set.
func TestEnvironmentBranchCLIHiddenUntilEnabled(t *testing.T) {
	t.Setenv("FAAS_ENVIRONMENT_BRANCHES_ENABLED", "")
	if environmentBranchVisible() {
		t.Fatal("hidden environment branch surfaced")
	}
	if code := main.CmdProjectsEnvironments([]string{"branch", "shop", "pr-1"}); code == 0 {
		t.Fatal("hidden environment branch dispatched")
	}
	t.Setenv("FAAS_ENVIRONMENT_BRANCHES_ENABLED", "1")
	if !environmentBranchVisible() {
		t.Fatal("enabled environment branch hidden")
	}
}

func TestEnvironmentBranchCLICreatesAndLists(t *testing.T) {
	var created api.CreateProjectEnvironmentBranchRequest
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			if err := json.NewDecoder(r.Body).Decode(&created); err != nil {
				t.Errorf("decode: %v", err)
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(api.ProjectEnvironmentResponse{Slug: created.Slug, Branch: &api.ProjectEnvironmentBranchResponse{
				Parent: "staging", BranchPoint: "2026-11-01T00:00:00Z", ExpiresAt: "2026-11-01T02:00:00Z", Egress: "live", Depth: 2,
				Workloads: []api.ProjectEnvironmentBranchWorkload{{Workload: "web", DeploymentID: "d-branch", SourceDeployment: "d-parent", Status: "snapshotting"}},
			}})
			return
		}
		_ = json.NewEncoder(w).Encode(api.ProjectEnvironmentBranchListResponse{Branches: []api.ProjectEnvironmentBranchSummary{{Slug: "pr-1", Parent: "staging", Depth: 2}}})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", os.Getenv("XDG_CONFIG_HOME"))
	t.Setenv("FAAS_ENVIRONMENT_BRANCHES_ENABLED", "1")
	var out bytes.Buffer
	old := *main.OsStdout
	*main.OsStdout = &out
	t.Cleanup(func() { *main.OsStdout = old })

	if code := main.CmdProjectsEnvironments([]string{"branch", "shop", "pr-1", "--from", "staging", "--ttl", "2h"}); code != 0 {
		t.Fatalf("branch exit = %d", code)
	}
	if created.Slug != "pr-1" || created.TTL != "2h" || !strings.Contains(out.String(), "web: deployment d-branch from d-parent") {
		t.Fatalf("request = %+v output = %s", created, out.String())
	}
	if code := main.CmdProjectsEnvironments([]string{"branch", "shop", "--list", "--from", "staging"}); code != 0 {
		t.Fatalf("list exit = %d", code)
	}
	want := []string{"POST /v1/projects/shop/environments/staging/branches", "GET /v1/projects/shop/environments/staging/branches"}
	if strings.Join(paths, ",") != strings.Join(want, ",") {
		t.Fatalf("paths = %v", paths)
	}
	if code := main.CmdProjectsEnvironments([]string{"branch", "shop", "pr-2", "--ttl", "5m"}); code == 0 {
		t.Fatal("a TTL under an hour was accepted")
	}
}
