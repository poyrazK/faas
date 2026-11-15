package main

import (
	"path/filepath"
	"testing"
)

// `alert slo status` and `alert check status` read the linked app like the
// other app-scoped commands instead of insisting on --app.
func TestFillLinkedAppDefaultsToTheLinkedApp(t *testing.T) {
	repo := t.TempDir()
	writeTargetFile(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/main\n")
	writeTargetFile(t, filepath.Join(repo, "gregale.yaml"), "project:\n  name: shop\n  app: shop-web\n")
	t.Chdir(repo)

	slug := ""
	fillLinkedApp(&slug)
	if slug != "shop-web" {
		t.Fatalf("empty --app = %q, want the linked shop-web", slug)
	}
	explicit := "billing"
	fillLinkedApp(&explicit)
	if explicit != "billing" {
		t.Fatalf("explicit --app replaced with %q", explicit)
	}

	t.Chdir(t.TempDir())
	none := ""
	fillLinkedApp(&none)
	if none != "" {
		t.Fatalf("no linked app, got %q", none)
	}
}
