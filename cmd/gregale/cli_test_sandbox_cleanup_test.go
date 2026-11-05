package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// Every cmd/gregale run left a ~0.7 GB gregale-cli-test-home-* behind: `go`
// subprocesses lost the developer's GOCACHE/GOTOOLCHAIN once HOME moved and
// filled the sandbox with a read-only module cache (Hunt 14 H14-45).
func TestCLITestSandboxKeepsGoCachesOutside(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go tool not on PATH")
	}
	home := os.Getenv("HOME")
	for _, key := range []string{"GOCACHE", "GOMODCACHE"} {
		value := os.Getenv(key)
		if value == "" || strings.HasPrefix(value, home) {
			t.Fatalf("%s = %q; want the developer's cache, outside the sandbox HOME %q", key, value, home)
		}
	}
}

func TestRemoveCLITestSandboxDeletesReadOnlyTrees(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sandbox")
	modules := filepath.Join(dir, "go", "pkg", "mod", "example.com", "m@v1.0.0")
	if err := os.MkdirAll(modules, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modules, "go.mod"), []byte("module example.com/m\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{modules, filepath.Dir(modules)} {
		if err := os.Chmod(path, 0o555); err != nil {
			t.Fatal(err)
		}
	}
	main.RemoveCLITestSandbox(dir)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("sandbox survived cleanup: %v", err)
	}
}
