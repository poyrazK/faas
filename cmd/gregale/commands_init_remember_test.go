package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// TestInitDeployRemembersTheApp pins that the app init --deploy created is
// the default for a later plain gregale deploy in that directory, instead of
// a new app named after the directory.
func TestInitDeployRemembersTheApp(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "node")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := main.LinkedDefaultAppName(dir); err != nil || got != "node" {
		t.Fatalf("before init: default name = %q, %v; want the directory name", got, err)
	}
	if err := main.RememberInitApp(dir, "orders-api"); err != nil {
		t.Fatal(err)
	}
	if got, err := main.LinkedDefaultAppName(dir); err != nil || got != "orders-api" {
		t.Fatalf("after init: default name = %q, %v; want orders-api", got, err)
	}
	ignore, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil || !strings.Contains(string(ignore), ".gregale") {
		t.Fatalf(".gitignore = %q, %v; want the local context ignored", ignore, err)
	}
}
