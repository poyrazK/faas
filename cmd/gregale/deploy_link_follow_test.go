package main

import (
	"os"
	"path/filepath"
	"testing"
)

// prod 2026-11-19: `deploy --name bh1119-probe` left the directory unlinked,
// so the next plain deploy created an app named after the directory and took
// probe.gregale.dev. A deploy that creates the app now links an unlinked
// directory to it.
func TestRememberCreatedAppLinksAnUnlinkedDirectory(t *testing.T) {
	dir := t.TempDir()
	rememberCreatedApp(dir, "bh-probe")
	link, _, err := targetProjectContext(dir)
	if err != nil || link.App != "bh-probe" {
		t.Fatalf("context = %+v, %v; want the created app remembered", link, err)
	}
}

func TestRememberCreatedAppKeepsAnExistingLink(t *testing.T) {
	dir := t.TempDir()
	if err := rememberInitApp(dir, "already-linked"); err != nil {
		t.Fatal(err)
	}
	rememberCreatedApp(dir, "other")
	link, _, err := targetProjectContext(dir)
	if err != nil || link.App != "already-linked" {
		t.Fatalf("context = %+v, %v; want the existing link kept", link, err)
	}
}

// prod 2026-11-19: after `app rename bh2-svc bh2-svc2` the checkout still
// named bh2-svc and its next deploy targeted a new app.
func TestRenameFollowsTheSavedContext(t *testing.T) {
	dir := t.TempDir()
	if err := rememberInitApp(dir, "old-name"); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "src")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	path, ok := followRenameInSavedContext(sub, "old-name", "new-name")
	if !ok || path != projectContextPath(dir) {
		t.Fatalf("path=%q ok=%v; want the saved context at the linked root updated", path, ok)
	}
	link, _, err := findProjectContext(dir)
	if err != nil || link.App != "new-name" {
		t.Fatalf("context = %+v, %v; want new-name", link, err)
	}
	if _, ok := followRenameInSavedContext(dir, "unrelated", "x"); ok {
		t.Fatal("a context naming another app was rewritten")
	}
}
