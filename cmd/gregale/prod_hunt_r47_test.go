package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Prod hunt r47: `gregale init --template hello-go my-app` printed only the
// usage; with the template named, the bare word is the directory.
func TestInitTakesDirectoryArgument(t *testing.T) {
	t.Setenv("FAAS_TOKEN", "test-token")
	for _, args := range [][]string{
		{"init", "--template", "hello-go", "--no-mcp", "my-app"},
		{"init", "my-app", "--template", "hello-go", "--no-mcp"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Chdir(t.TempDir())
			code, stdout, stderr := runCLI(t, args...)
			if code != 0 {
				t.Fatalf("init = %d %q %q", code, stdout, stderr)
			}
			if _, err := os.Stat(filepath.Join("my-app", "go.mod")); err != nil {
				t.Fatalf("template not written to my-app: %v", err)
			}
		})
	}
}

// Prod hunt r47: --list offered the customer-operation starters, which need
// an SDK tarball customers cannot get.
func TestInitListLeavesOutUnpublishedSDKStarters(t *testing.T) {
	_, stdout, _ := runCLI(t, "init", "--list")
	if strings.Contains(stdout, "customer-operation") {
		t.Fatalf("init --list offers customer-operation starters:\n%s", stdout)
	}
}
