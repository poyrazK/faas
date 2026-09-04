// names_test.go — coverage for pkg/releaseinstall/names.go (ADR-113
// canonical daemon tarball, PR-A commit 1).
//
// Whitebox test (package releaseinstall, mirroring pkg/role/role_test.go
// and pkg/statefuldenylist/match_test.go). The functions tested are
// pure — linear scans of catalog slices plus a single switch in
// executableName. NamesTest uses t.TempDir() + os.WriteFile for the
// resolveBinary file-system cases.
package releaseinstall

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// TestSupportBinaryNames_Stable asserts the catalog is returned in
// stable source order across calls. Stable order matters because the
// release bundle's bin-tree walk and the manifest's daemon list
// share this order.
func TestSupportBinaryNames_Stable(t *testing.T) {
	first := SupportBinaryNames()
	if len(first) == 0 {
		t.Fatal("SupportBinaryNames returned empty slice")
	}
	second := SupportBinaryNames()
	if len(first) != len(second) {
		t.Fatalf("SupportBinaryNames length drifted: %d → %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Errorf("SupportBinaryNames[%d] = %q, want %q", i, second[i], first[i])
		}
	}
	// The canonical support files — every release ships these.
	want := []string{"gregale", "gregalectl", "init", "schedd-brokerq-apply", "vmmd-raw-bridge", "vmmd-stream-bridge", "vmlinux"}
	for _, name := range want {
		found := false
		for _, got := range first {
			if got == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("SupportBinaryNames missing %q (got %v)", name, first)
		}
	}
}

func TestLegacyUnhashedSupportBinaryNames_IsNarrow(t *testing.T) {
	got := LegacyUnhashedSupportBinaryNames()
	if len(got) != 1 || got[0] != "init" {
		t.Fatalf("legacy compatibility catalog = %v, want [init]", got)
	}
	if !IsLegacyUnhashedSupportBinaryName("init") {
		t.Fatal("init is not recognised as a legacy support binary")
	}
	if IsLegacyUnhashedSupportBinaryName("gregalectl") || IsLegacyUnhashedSupportBinaryName("rogue") {
		t.Fatal("legacy compatibility catalog accepted an unrelated support file")
	}
}

// TestRuntimeAssetNames_Stable asserts the runtime-asset catalog is
// stable across calls and includes the six canonical runner paths
// (go124, go124-alpine, node22, node24, python312, python313).
func TestRuntimeAssetNames_Stable(t *testing.T) {
	got := RuntimeAssetNames()
	want := []string{
		"runners/go124/faas-runner",
		"runners/go124-alpine/faas-runner",
		"runners/node22/faas-runner",
		"runners/node24/faas-runner",
		"runners/python312/faas-runner",
		"runners/python313/faas-runner",
	}
	if len(got) != len(want) {
		t.Fatalf("RuntimeAssetNames length = %d, want %d (got %v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("RuntimeAssetNames[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestIsRuntimeAssetName_Cases is a table-driven sweep over every
// accepted runner and a few rejections. The Is prefix is the
// canonical seam the bin-tree walker uses to decide "is this entry
// expected in the tarball".
func TestIsRuntimeAssetName_Cases(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"runners/go124/faas-runner", true},
		{"runners/python313/faas-runner", true},
		{"runners/node22/faas-runner", true},
		{"runners/node24/faas-runner", true},
		{"runners/python312/faas-runner", true},
		{"runners/go124-alpine/faas-runner", true},
		// Rejections — anything outside the catalog.
		{"gregale", false},
		{"runners/ruby33/faas-runner", false},
		{"runners/go124", false}, // missing /faas-runner suffix
		{"", false},
		{"/runners/go124/faas-runner", false}, // leading slash not accepted
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			if got := IsRuntimeAssetName(tc.in); got != tc.want {
				t.Errorf("IsRuntimeAssetName(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestIsCatalogBinaryName_Cases covers the catalog-or-canonical-
// executable matcher. The matcher accepts the logical key
// (gatewayd_internal) AND its dashed canonical name
// (gatewayd-internal) for the two gateway daemons, and only the
// logical key for the rest. The dashed gateway form is the new
// canonical name (ADR-113) but legacy tooling still emits underscored
// logical keys.
func TestIsCatalogBinaryName_Cases(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"gatewayd_internal", true},
		{"gatewayd-internal", true},
		{"gatewayd_public", true},
		{"gatewayd-public", true},
		{"apid", true},
		{"schedd", true},
		{"vmmd", true},
		{"builderd", true},
		{"imaged", true},
		{"meterd", true},
		{"githubd", true},
		// Rejections.
		{"migrate", false},
		{"faas-nft-render", false}, // helper, not a daemon
		{"gatewayd", false},        // missing _internal or _public suffix
		{"", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			if got := IsCatalogBinaryName(tc.in); got != tc.want {
				t.Errorf("IsCatalogBinaryName(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestIsReleaseBinaryName_Disjunction walks the union: a string is
// a release binary iff it is a catalog daemon OR one of the support
// binaries. The two sets are disjoint, so this is straightforward to
// verify by walking both lists.
func TestIsReleaseBinaryName_Disjunction(t *testing.T) {
	for _, catalog := range []string{"apid", "schedd", "vmmd", "gatewayd-internal", "gatewayd-public"} {
		if !IsReleaseBinaryName(catalog) {
			t.Errorf("IsReleaseBinaryName(%q) = false, want true (catalog)", catalog)
		}
	}
	for _, support := range SupportBinaryNames() {
		if !IsReleaseBinaryName(support) {
			t.Errorf("IsReleaseBinaryName(%q) = false, want true (support)", support)
		}
	}
	// Pathological inputs must be rejected.
	for _, notARelease := range []string{"README.md", "license.txt", "/tmp/foo", ""} {
		if IsReleaseBinaryName(notARelease) {
			t.Errorf("IsReleaseBinaryName(%q) = true, want false", notARelease)
		}
	}
}

// TestExecutableName_Switch is the branch-coverage table for the
// unexported helper. Hits every case of the switch: gatewayd_internal
// (→ dashed), gatewayd_public (→ dashed), and the default passthrough.
func TestExecutableName_Switch(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"gatewayd_internal", "gatewayd-internal"},
		{"gatewayd_public", "gatewayd-public"},
		{"apid", "apid"},
		{"schedd", "schedd"},
		{"vmmd", "vmmd"},
		{"gatewayd", "gatewayd"}, // unknown gateway form, no transform
		{"", ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			if got := executableName(tc.in); got != tc.want {
				t.Errorf("executableName(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestResolveBinary_FindsCanonical covers the happy path: the
// canonical (dashed) executable filename exists, resolveBinary returns
// it. Uses t.TempDir() to plant a fake release bundle.
func TestResolveBinary_FindsCanonical(t *testing.T) {
	dir := t.TempDir()
	canonical := filepath.Join(dir, "gatewayd-internal")
	if err := os.WriteFile(canonical, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write canonical: %v", err)
	}
	got, err := resolveBinary(dir, "gatewayd_internal")
	if err != nil {
		t.Fatalf("resolveBinary: %v", err)
	}
	if got != canonical {
		t.Errorf("resolveBinary returned %q, want %q", got, canonical)
	}
}

// TestResolveBinary_FindsLogical covers the compat path: the canonical
// file is absent but the underscored logical name is present (legacy
// tooling). resolveBinary still returns a usable path; the order
// matches names.go's candidate list (logical first, canonical second).
func TestResolveBinary_FindsLogical(t *testing.T) {
	dir := t.TempDir()
	logical := filepath.Join(dir, "gatewayd_internal")
	if err := os.WriteFile(logical, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write logical: %v", err)
	}
	got, err := resolveBinary(dir, "gatewayd_internal")
	if err != nil {
		t.Fatalf("resolveBinary: %v", err)
	}
	if got != logical {
		t.Errorf("resolveBinary returned %q, want %q", got, logical)
	}
}

// TestResolveBinary_MissingReturnsError covers the "neither candidate
// exists" branch. The contract is documented in names.go: the returned
// path is the logical candidate plus the first stat error, so callers
// see where the tool looked.
func TestResolveBinary_MissingReturnsError(t *testing.T) {
	dir := t.TempDir()
	_, err := resolveBinary(dir, "apid")
	if err == nil {
		t.Fatal("expected error for missing apid binary")
	}
}

// TestResolveBinary_RejectsDirectory covers the "candidate exists but
// is a directory" branch. resolveBinary returns ErrInvalid via
// os.PathError so the caller knows not to exec into a directory.
func TestResolveBinary_RejectsDirectory(t *testing.T) {
	dir := t.TempDir()
	// Plant a DIRECTORY named like the canonical executable.
	if err := os.Mkdir(filepath.Join(dir, "apid"), 0o755); err != nil {
		t.Fatalf("mkdir apid: %v", err)
	}
	_, err := resolveBinary(dir, "apid")
	if err == nil {
		t.Fatal("expected error for directory-instead-of-binary")
	}
}

// TestSortedRuntimeAssetNames asserts the helper that returns a
// sorted copy of the runner catalog — used by tarball hashing so the
// hash doesn't depend on the bin-tree walk order.
func TestSortedRuntimeAssetNames(t *testing.T) {
	got := sortedRuntimeAssetNames()
	want := RuntimeAssetNames()
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("sortedRuntimeAssetNames length = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("sortedRuntimeAssetNames[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
