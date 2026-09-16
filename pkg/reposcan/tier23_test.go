package reposcan

import (
	"sort"
	"strings"
	"testing"
	"testing/fstest"
)

// TestDetectWorkspaces_PnpmMonorepo covers the canonical pnpm
// workspace shape: a top-level packages/* glob with runnable services, a
// worker, and multiple shared libraries.
// Runnable members carry a Dockerfile or executable package target.
// Package libraries and README-only dirs are filtered.
func TestDetectWorkspaces_PnpmMonorepo(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"pnpm-workspace.yaml": &fstest.MapFile{
			Data: []byte("packages:\n  - \"packages/*\"\n"),
		},
		"packages/api/Dockerfile":      &fstest.MapFile{Data: []byte("FROM scratch")},
		"packages/web/Dockerfile":      &fstest.MapFile{Data: []byte("FROM scratch")},
		"packages/worker/package.json": &fstest.MapFile{Data: []byte(`{"scripts":{"worker":"node worker.js"}}`)},
		"packages/lib/package.json":    &fstest.MapFile{Data: []byte("{}")},
		"packages/shared/package.json": &fstest.MapFile{Data: []byte(`{"main":"index.js"}`)},
		"packages/docs/README.md":      &fstest.MapFile{Data: []byte("# docs\n")},
	}
	seeds, warnings, err := detectWorkspaces(fsys)
	if err != nil {
		t.Fatalf("detectWorkspaces: %v", err)
	}
	namesGot := make([]string, len(seeds))
	for i, s := range seeds {
		namesGot[i] = s.name
	}
	sort.Strings(namesGot)
	if !equalSet(namesGot, []string{"api", "web", "worker"}) {
		t.Errorf("seed names = %v, want {api,web,worker}", namesGot)
	}
	if len(warnings) != 2 || !strings.Contains(strings.Join(warnings, "\n"), "packages/lib") ||
		!strings.Contains(strings.Join(warnings, "\n"), "packages/shared") {
		t.Errorf("warnings = %v, want both library skip explanations", warnings)
	}
	for _, seed := range seeds {
		if seed.name == "worker" && (seed.class != ClassWorker || !seed.commandShell) {
			t.Fatalf("worker seed = %#v", seed)
		}
	}
}

// TestDetectWorkspaces_TurboRepo — turbo.json pipeline keys
// emit workloads keyed by name (not directory path).
func TestDetectWorkspaces_TurboRepo(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"turbo.json": &fstest.MapFile{
			Data: []byte("{\"pipeline\":{\"build\":{},\"lint\":{},\"test\":{}}}\n"),
		},
	}
	seeds, _, err := detectWorkspaces(fsys)
	if err != nil {
		t.Fatalf("detectWorkspaces: %v", err)
	}
	// turbo.json pipeline keys aren't directories by default; the
	// marker check is satisfied if a file with the same name as
	// the key exists at root. None exist here, so no workloads.
	// That's correct — Turbo relies on naming conventions
	// (apps/* / packages/*) above the pipeline.
	if len(seeds) != 0 {
		t.Errorf("expected 0 seeds for bare turbo.json with no marker dirs, got %v",
			names(seeds))
	}
}

// TestDetectConvention_DotDirs — services/auth/ + services/payments/
// both have a Dockerfile → 2 workloads; services/lib/ with
// package.json is also a workload (package.json is a valid
// language marker — see §3); services/empty (no marker at all)
// is dropped.
func TestDetectConvention_DotDirs(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"services/auth/Dockerfile":     &fstest.MapFile{Data: []byte("FROM scratch")},
		"services/payments/Dockerfile": &fstest.MapFile{Data: []byte("FROM scratch")},
		"services/lib/package.json":    &fstest.MapFile{Data: []byte("{}")},
		"services/empty/README.md":     &fstest.MapFile{Data: []byte("# nothing here\n")},
	}
	seeds, warnings, err := detectConvention(fsys)
	if err != nil {
		t.Fatalf("detectConvention: %v", err)
	}
	namesGot := make([]string, len(seeds))
	for i, s := range seeds {
		namesGot[i] = s.name
	}
	sort.Strings(namesGot)
	if !equalSet(namesGot, []string{"auth", "payments"}) {
		t.Errorf("seed names = %v, want {auth,payments}", namesGot)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "services/lib") {
		t.Errorf("warnings = %v, want skipped library explanation", warnings)
	}
}

// TestDetectConvention_AppsAndPkgs — both `apps/` and `packages/`
// are walked; members with go.mod, pyproject.toml, Cargo.toml, or
// pom.xml are eligible too.
func TestDetectConvention_AppsAndPkgs(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"apps/web/Dockerfile":     &fstest.MapFile{Data: []byte("FROM scratch")},
		"apps/api/go.mod":         &fstest.MapFile{Data: []byte("module api\n")},
		"packages/cli/Cargo.toml": &fstest.MapFile{Data: []byte("[package]\nname = \"cli\"\n")},
		"packages/old/README.md":  &fstest.MapFile{Data: []byte("# old\n")},
	}
	seeds, _, err := detectConvention(fsys)
	if err != nil {
		t.Fatalf("detectConvention: %v", err)
	}
	namesGot := make([]string, len(seeds))
	for i, s := range seeds {
		namesGot[i] = s.name
	}
	sort.Strings(namesGot)
	want := []string{"web", "api", "cli"}
	if !equalSet(namesGot, want) {
		t.Errorf("seed names = %v, want %v (README-only dir excluded)", namesGot, want)
	}
}

// TestTierFloor_BareRepo — only a Dockerfile at root. Tiers 1-3
// produce no workloads; Scan() must emit the Tier-4 root-floor
// workload `name="app"`.
func TestTierFloor_BareRepo(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"Dockerfile": &fstest.MapFile{Data: []byte("FROM scratch")},
		"main.go":    &fstest.MapFile{Data: []byte("package main\n")},
	}
	r, err := Scan(fsys)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(r.Workloads) != 1 || r.Workloads[0].Name != "app" {
		t.Errorf("workloads = %v, want one app", r.Workloads)
	}
	if r.Workloads[0].Tier != TierSingle {
		t.Errorf("tier = %s, want single", r.Workloads[0].Tier)
	}
}
