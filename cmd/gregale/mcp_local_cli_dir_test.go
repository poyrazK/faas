package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// The MCP server's CLI commands run in a fresh directory under the user's
// cache directory, never in the shared temp directory: the CLI reads the
// nearest .gregale/project.json above its working directory, and another
// user can plant one in /tmp.
func TestMCPLocalCLIRunsInAPrivateDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	first, err := main.McpLocalCLIDir()
	if err != nil {
		t.Fatal(err)
	}
	second, err := main.McpLocalCLIDir()
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("two commands share %s", first)
	}
	for _, dir := range []string{first, second} {
		if !strings.HasPrefix(dir, cache+string(filepath.Separator)) {
			t.Fatalf("command directory %s is outside the user's cache %s", dir, cache)
		}
	}
	info, err := os.Stat(filepath.Dir(first))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("command directories' parent has mode %v, want no group or other access", info.Mode().Perm())
	}
}
