package githubd

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestFilterIgnoredGitHubPaths(t *testing.T) {
	policy := state.DefaultGitHubDeployPolicy("project", "account")
	policy.IgnoredPaths = []string{"docs/**", "README.md"}
	got := filterIgnoredGitHubPaths([]string{"docs/guide.md", "README.md", "src/main.go"}, policy)
	if len(got) != 1 || got[0] != "src/main.go" {
		t.Fatalf("filtered paths = %#v, want [src/main.go]", got)
	}
}

func TestApplyGitHubRootPolicyDoesNotOverrideWorkloadRoot(t *testing.T) {
	policy := state.DefaultGitHubDeployPolicy("project", "account")
	policy.RootDir = "apps/web"
	root := applyGitHubRootPolicy(state.App{RootDir: ""}, policy)
	if root.RootDir != "apps/web" {
		t.Fatalf("root dir = %q, want apps/web", root.RootDir)
	}
	explicit := applyGitHubRootPolicy(state.App{RootDir: "services/api"}, policy)
	if explicit.RootDir != "services/api" {
		t.Fatalf("explicit root dir = %q, want services/api", explicit.RootDir)
	}
}
