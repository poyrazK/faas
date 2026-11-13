package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTargetFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ADR-20261107180000 slice 3: a fresh clone targets the committed project and
// app without `gregale link`; a service directory names its own app.
func TestTargetProjectContextFromCommittedManifest(t *testing.T) {
	repo := t.TempDir()
	writeTargetFile(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/main\n")
	writeTargetFile(t, filepath.Join(repo, "gregale.yaml"), "project:\n  name: shop\n  app: shop-web\n")
	writeTargetFile(t, filepath.Join(repo, "services", "billing", "gregale.yaml"), "project:\n  app: shop-billing\n")

	root, _, err := targetProjectContext(repo)
	if err != nil || root.Project != "shop" || root.App != "shop-web" || root.Version != projectContextVersion {
		t.Fatalf("root target = %+v err=%v", root, err)
	}
	billing, source, err := targetProjectContext(filepath.Join(repo, "services", "billing"))
	if err != nil || billing.Project != "shop" || billing.App != "shop-billing" {
		t.Fatalf("billing target = %+v err=%v", billing, err)
	}
	if !strings.HasSuffix(source, filepath.Join("services", "billing", "gregale.yaml")) {
		t.Fatalf("source = %q, want the service manifest", source)
	}
	if app, err := linkedAppSlug(filepath.Join(repo, "services", "billing")); err != nil || app != "shop-billing" {
		t.Fatalf("linkedAppSlug = %q, %v", app, err)
	}
}

func TestTargetProjectContextLinkFileWins(t *testing.T) {
	repo := t.TempDir()
	writeTargetFile(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/main\n")
	writeTargetFile(t, filepath.Join(repo, "gregale.yaml"), "project:\n  name: shop\n  app: shop-web\n")
	writeTargetFile(t, filepath.Join(repo, ".gregale", "project.json"), `{"version":1,"project":"shop-staging","app":"web","environment":"staging"}`)
	got, _, err := targetProjectContext(repo)
	if err != nil || got.Project != "shop-staging" || got.App != "web" || got.Environment != "staging" {
		t.Fatalf("target = %+v err=%v, want the local link", got, err)
	}
	// Commands that rewrite or delete the link file never see the manifest.
	if _, path, err := findProjectContext(repo); err != nil || filepath.Base(path) != "project.json" {
		t.Fatalf("findProjectContext path = %q err=%v", path, err)
	}
}

func TestTargetProjectContextStandaloneAndAbsent(t *testing.T) {
	repo := t.TempDir()
	writeTargetFile(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/main\n")
	if _, _, err := targetProjectContext(repo); !errors.Is(err, errProjectContextNotFound) {
		t.Fatalf("no manifest: err = %v, want errProjectContextNotFound", err)
	}
	writeTargetFile(t, filepath.Join(repo, "gregale.yaml"), "project:\n  app: my-api\n")
	got, _, err := targetProjectContext(repo)
	if err != nil || got.Version != standaloneContextVersion || got.App != "my-api" || got.Project != "" {
		t.Fatalf("standalone target = %+v err=%v", got, err)
	}
	writeTargetFile(t, filepath.Join(repo, "gregale.yaml"), "project:\n  name: Not_A_Slug\n")
	if _, _, err := targetProjectContext(repo); err == nil || !strings.Contains(err.Error(), "project: name/app") {
		t.Fatalf("invalid slug: err = %v", err)
	}
}

// The walk stops at the Git root: a manifest above the repository is not
// this checkout's target.
func TestTargetProjectContextStopsAtGitRoot(t *testing.T) {
	outer := t.TempDir()
	writeTargetFile(t, filepath.Join(outer, "gregale.yaml"), "project:\n  name: outer\n")
	repo := filepath.Join(outer, "repo")
	writeTargetFile(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/main\n")
	if _, _, err := targetProjectContext(repo); !errors.Is(err, errProjectContextNotFound) {
		t.Fatalf("err = %v, want errProjectContextNotFound", err)
	}
}
