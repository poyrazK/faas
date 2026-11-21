package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// GREG-05: a project of digest-pinned images was told
// "framework_not_detected ... supply an explicit Dockerfile or command" by
// the deploy preflight, which profiled the Compose root as if it were one
// app. The preflight runs before any request, so an unreachable API is
// enough to observe it.
func TestProjectDeployPreflightSkipsRootFrameworkProfile(t *testing.T) {
	dir := t.TempDir()
	compose := "services:\n  web:\n    image: nginx:1.27@sha256:" + strings.Repeat("a", 64) + "\n    ports: [\"8080:80\"]\n"
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(compose), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAAS_API", "http://127.0.0.1:1")
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	main.WithResetJSONOutput(t, false)
	stdout, stderr, restore := main.SwapIO(t)
	main.CmdDeployTarball([]string{"--project", "--name", "demo", "--path", dir, "--source=worktree", "--dry-run"})
	restore()
	if out := stdout.String() + stderr(); strings.Contains(out, "framework_not_detected") || strings.Contains(out, "profile warnings") {
		t.Fatalf("project preflight profiled the Compose root:\n%s", out)
	}
}
