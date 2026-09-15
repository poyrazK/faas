package e2e_test

// No metal test may point imaged at a tag-based builder base.
//
// imaged requires FAAS_BUILDER_BASE_REF to be digest-pinned and EXITS at boot
// otherwise:
//
//	imaged: FAAS_BUILDER_BASE_REF "127.0.0.1:37413/onebox-faas/builder-base:latest"
//	must be a digest-pinned reference
//
// Ten metal tests set a ":latest" tag against the in-process fake registry, so
// imaged died at startup in every one of them. Nothing checked that imaged was
// alive, so the failure surfaced minutes later as a deployment that never left
// `building` — a timeout, attributed to the deploy rather than the daemon.
//
// FakeRegistry.AddImage already returns the digest-pinned ref; those tests
// discarded it with `_ =` and hand-built a tag instead.
//
// Untagged deliberately: the tests it guards are metal-only, but this is a
// string check over their source and belongs in ordinary CI.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestNoMetalTestUsesATagBasedBuilderBaseRef(t *testing.T) {
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}

	// Matches a FAAS_TEST_BUILDER_BASE_REF assignment whose value is built
	// from a string literal rather than a digest-pinned ref.
	tagged := regexp.MustCompile(`FAAS_TEST_BUILDER_BASE_REF"[^\n]*:latest`)

	var bad []string
	for _, f := range files {
		// This file quotes the offending line in its own doc comment.
		if f == "builder_base_ref_test.go" {
			continue
		}
		src, readErr := os.ReadFile(f)
		if readErr != nil {
			t.Fatalf("read %s: %v", f, readErr)
		}
		if tagged.Match(src) {
			bad = append(bad, f)
		}
	}
	if len(bad) > 0 {
		t.Errorf("these tests set a tag-based FAAS_TEST_BUILDER_BASE_REF, which makes "+
			"imaged exit at boot and surfaces later as a deploy timeout: %s\n"+
			"Use the digest-pinned ref returned by registry.AddImage.",
			strings.Join(bad, ", "))
	}
}

// The digest-pinned ref must actually be used, not merely produced — an
// AddImage whose result is discarded is what created this bug.
func TestBuilderBaseRefComesFromAddImage(t *testing.T) {
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	discarded := regexp.MustCompile(`_\s*=\s*registry\.AddImage\("onebox-faas/builder-base"`)

	var bad []string
	for _, f := range files {
		src, readErr := os.ReadFile(f)
		if readErr != nil {
			t.Fatalf("read %s: %v", f, readErr)
		}
		if !strings.Contains(string(src), "FAAS_TEST_BUILDER_BASE_REF") {
			continue
		}
		if discarded.Match(src) {
			bad = append(bad, f)
		}
	}
	if len(bad) > 0 {
		t.Errorf("these tests discard the digest-pinned ref from AddImage while still "+
			"setting FAAS_TEST_BUILDER_BASE_REF: %s", strings.Join(bad, ", "))
	}
}
