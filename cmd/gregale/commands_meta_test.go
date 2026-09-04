// commands_meta_test.go — pins the CLI manifest's flag-level ClosedSet
// literals against the embedded templates.FS. Catches drift in both
// directions so adding a template to one without updating the other
// fails CI before it ships.
//
// Issue #961 / Mega-B PR-2 — replaces the previous 'node22-http' /
// 'python312-http' literals with the canonical 15-name catalog. This
// test is the load-bearing safety net that locks the manifest in sync
// with templates.Names going forward.
//
// Why not extend commands_completion_test.go::TestCompletion_ManifestDrift:
// that test walks main.go's switch and asserts command-level parity.
// Flag-level ClosedSet literals are a different audit (a flag can have
// a closed set without the command being missing), so a dedicated test
// is clearer than overloading the manifest-drift test.
package main

import (
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale/templates"
)

// TestClosedSetTemplatesMatchEmbedFS asserts that every --template
// ClosedSet literal in cli_meta.go references a name that exists in
// templates.Names AND vice versa. Catches both directions:
//
//   - ClosedSet has "node22-http" but templates.Names does not → FAIL.
//   - templates.Names has "foo" but ClosedSet does not → FAIL.
func TestClosedSetTemplatesMatchEmbedFS(t *testing.T) {
	closedSet := map[string]struct{}{}
	for _, c := range cliCommands {
		if c.Name != "deploy" && c.Name != "init" {
			continue
		}
		for _, f := range c.Flags {
			if f.Name == "template" {
				for _, n := range f.ClosedSet {
					closedSet[n] = struct{}{}
				}
			}
		}
	}
	embedSet := map[string]struct{}{}
	for _, n := range templates.Names {
		embedSet[n] = struct{}{}
	}
	if len(closedSet) == 0 {
		t.Fatalf("--template ClosedSet is empty in cli_meta.go (expected the 15-name catalog)")
	}
	if len(closedSet) != len(embedSet) {
		t.Errorf("ClosedSet size = %d, templates.Names size = %d", len(closedSet), len(embedSet))
	}
	for n := range closedSet {
		if _, ok := embedSet[n]; !ok {
			t.Errorf("--template ClosedSet has %q but templates.Names does not", n)
		}
	}
	for n := range embedSet {
		if _, ok := closedSet[n]; !ok {
			t.Errorf("templates.Names has %q but --template ClosedSet does not", n)
		}
	}
}

// TestTemplateNames13MirrorsEmbedFS asserts the const referenced by
// both ClosedSet literals (templateNames13) matches templates.Names
// byte-for-byte (same length, same order). Catches accidental reorders
// in the catalog — the completion-backend ordering is part of the
// customer-facing contract.
func TestTemplateNames13MirrorsEmbedFS(t *testing.T) {
	if len(templateNames13) != len(templates.Names) {
		t.Fatalf("templateNames13 len = %d, templates.Names len = %d", len(templateNames13), len(templates.Names))
	}
	for i, want := range templates.Names {
		if got := templateNames13[i]; got != want {
			t.Errorf("templateNames13[%d] = %q, want %q", i, got, want)
		}
	}
}
