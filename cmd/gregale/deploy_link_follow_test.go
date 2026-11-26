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

// Prod hunt r50: `deploy --name bh53-redis` under a directory whose own link
// named bh50-go saved nothing, so later commands in the new checkout targeted
// bh50-go, and `link --app bh53-redis` rewrote the parent's link. The new app
// is now linked in its own directory and the parent's link is kept.
func TestCreatedAppUnderAnotherAppsLinkGetsItsOwnLink(t *testing.T) {
	parent := t.TempDir()
	if err := rememberInitApp(parent, "parent-app"); err != nil {
		t.Fatal(err)
	}
	created := filepath.Join(parent, "created")
	linked := filepath.Join(parent, "linked")
	for _, dir := range []string{created, linked} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	rememberCreatedApp(created, "new-app")

	t.Chdir(linked)
	if code := linkStandaloneApp("linked-app", true); code != 0 {
		t.Fatalf("link exit = %d", code)
	}

	for dir, want := range map[string]string{parent: "parent-app", created: "new-app", linked: "linked-app"} {
		link, path, err := targetProjectContext(dir)
		if err != nil || link.App != want {
			t.Errorf("%s targets %+v (%s, %v), want %s", dir, link, path, err, want)
		}
	}
}

// A directory under a project link keeps deploying within that project.
func TestCreatedAppUnderAProjectLinkKeepsIt(t *testing.T) {
	parent := t.TempDir()
	if _, err := saveProjectContext(parent, localProjectContext{Version: projectContextVersion, Project: "shop"}); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(parent, "svc")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	rememberCreatedApp(sub, "new-app")
	if _, err := os.Stat(projectContextPath(sub)); !os.IsNotExist(err) {
		t.Fatalf("saved a link in the project's subdirectory: %v", err)
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
