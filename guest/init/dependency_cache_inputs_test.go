package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// The digest follows the plan and the install inputs, not the rest of the
// source: a source edit keeps it, a lockfile edit or a new plan changes it.
func TestDependencyInputsDigest(t *testing.T) {
	plan, err := os.ReadFile("../../pkg/devpatch/testdata/railpack-0.38.0-node-build.json")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"package.json": `{"name":"x"}`, "package-lock.json": "{}", "index.js": "v1"})
	base, err := dependencyInputsDigest("frontend@1", plan, dir)
	if err != nil {
		t.Fatal(err)
	}
	digest := func() string {
		t.Helper()
		got, err := dependencyInputsDigest("frontend@1", plan, dir)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	writeTree(t, dir, map[string]string{"index.js": "v2", "src/app.ts": "edit"})
	if got := digest(); got != base {
		t.Fatal("a source edit changed the dependency inputs")
	}
	writeTree(t, dir, map[string]string{"railpack.json": `{"deploy":{}}`})
	withConfig := digest()
	if withConfig == base {
		t.Fatal("a new railpack.json did not change the dependency inputs")
	}
	writeTree(t, dir, map[string]string{"package-lock.json": `{"lockfileVersion":3}`})
	if got := digest(); got == withConfig {
		t.Fatal("a lockfile edit did not change the dependency inputs")
	}
	if other, _ := dependencyInputsDigest("frontend@2", plan, dir); other == digest() {
		t.Fatal("a new frontend did not change the dependency inputs")
	}
	if _, err := dependencyInputsDigest("frontend@1", []byte("{"), dir); err == nil {
		t.Fatal("an unreadable plan was hashed")
	}
}

// adr: 740 — the helper keeps an imported cache built from the same inputs
// and exports otherwise; the digest then travels inside the exported cache.
func TestRunDependencyCacheInputs(t *testing.T) {
	plan, err := os.ReadFile("../../pkg/devpatch/testdata/railpack-0.38.0-node-build.json")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	src, imported, out := filepath.Join(root, "src"), filepath.Join(root, "cache"), filepath.Join(root, "out")
	writeTree(t, root, map[string]string{"plan.json": string(plan), "src/package.json": "{}", "src/package-lock.json": "{}"})
	for _, d := range []string{imported, out} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	run := func() string {
		t.Helper()
		var stdout strings.Builder
		runDependencyCacheInputs("frontend@1", []string{filepath.Join(root, "plan.json"), src, imported, out}, &stdout)
		return strings.TrimSpace(stdout.String())
	}
	if got := run(); got != "export" || dependencyCacheKept(out) {
		t.Fatalf("cold build answered %q", got)
	}
	// The exported cache carries the digest into the next build.
	writeTree(t, out, map[string]string{"cache/index.json": "{}"})
	finishDependencyCacheInputs(out)
	recorded, err := os.ReadFile(filepath.Join(out, "cache", dependencyInputsFile))
	if err != nil {
		t.Fatal(err)
	}
	writeTree(t, imported, map[string]string{dependencyInputsFile: string(recorded)})
	if err := os.RemoveAll(out); err != nil || os.MkdirAll(out, 0o755) != nil {
		t.Fatal(err)
	}
	if got := run(); got != "keep" || !dependencyCacheKept(out) {
		t.Fatalf("unchanged inputs answered %q", got)
	}
	writeTree(t, src, map[string]string{"package-lock.json": `{"changed":true}`})
	if err := os.RemoveAll(out); err != nil || os.MkdirAll(out, 0o755) != nil {
		t.Fatal(err)
	}
	if got := run(); got != "export" || dependencyCacheKept(out) {
		t.Fatalf("changed lockfile answered %q", got)
	}
	var stdout strings.Builder
	if runDependencyCacheInputs("frontend@1", []string{"missing-plan.json", src, imported, out}, &stdout); strings.TrimSpace(stdout.String()) != "export" {
		t.Fatalf("missing plan answered %q, want export", stdout.String())
	}
	// A discarded cache keeps no stray digest in the build output.
	finishDependencyCacheInputs(out)
	if _, err := os.Stat(filepath.Join(out, dependencyInputsFile)); !os.IsNotExist(err) {
		t.Fatalf("digest left without a cache: %v", err)
	}
}
