package main

// Test file for issue #740 / DEPLOY-PROV-5 — pins the
// `gregale build provenance <id>` rendering of the new
// framework_version row (issue #740 / ADR-087). The print function
// is whitebox (package main) on purpose: the row slice is the load-
// bearing contract for the row label + value, and the test must
// catch silent reordering of the fields. The minimum viable test
// is a single row scan that asserts the new row is present at the
// expected position.

import (
	"bytes"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestPrintProvenance_IncludesFrameworkVersion pins the new
// framework_version row at the position between railpack_version and
// base_digest (mirrors the build_provenance column order).
func TestPrintProvenance_IncludesFrameworkVersion(t *testing.T) {
	var buf bytes.Buffer
	printProvenance(&buf, api.BuildProvenanceResponse{
		ID:           "id-1",
		BuildID:      "build-1",
		BuildkitVer:  "",
		RailpackVer:  "",
		FrameworkVer: "22.11.0",
		BaseDigest:   "sha256:abc",
	})
	out := buf.String()
	// The row label is left-padded to 22 chars then ": " then the
	// value. "framework_version" is 17 chars, so the column is padded
	// to 22 → "framework_version: " (5-char gutter). The assertion
	// below groups the prefix + value with the full padding.
	wantLine := "framework_version:     22.11.0"
	if !strings.Contains(out, wantLine) {
		t.Errorf("expected line %q in output; got:\n%s", wantLine, out)
	}
	if !strings.Contains(out, "railpack_version:") {
		t.Errorf("expected railpack_version row to remain; got:\n%s", out)
	}
	if !strings.Contains(out, "base_digest:") {
		t.Errorf("expected base_digest row to remain; got:\n%s", out)
	}
	// Order: railpack_version must come before framework_version,
	// which must come before base_digest. The test fails on silent
	// reordering.
	idxRail := strings.Index(out, "railpack_version:")
	idxFW := strings.Index(out, "framework_version:")
	idxBase := strings.Index(out, "base_digest:")
	if !(idxRail < idxFW && idxFW < idxBase) {
		t.Errorf("row order: railpack=%d, framework=%d, base=%d; want railpack < framework < base",
			idxRail, idxFW, idxBase)
	}
}

// TestPrintProvenance_EmptyFrameworkVersionRendersEmptyRow pins the
// empty case: a build with no version detected (no .nvmrc, no
// engines.node, no .python-version, etc.) renders the
// framework_version row with an empty value, matching the
// buildkit_version / railpack_version convention (auditor wants
// line consistency, not skipped rows).
func TestPrintProvenance_EmptyFrameworkVersionRendersEmptyRow(t *testing.T) {
	var buf bytes.Buffer
	printProvenance(&buf, api.BuildProvenanceResponse{
		BuildID:      "build-2",
		FrameworkVer: "",
	})
	out := buf.String()
	wantLine := "framework_version:"
	if !strings.Contains(out, wantLine) {
		t.Errorf("expected empty framework_version row; got:\n%s", out)
	}
}
