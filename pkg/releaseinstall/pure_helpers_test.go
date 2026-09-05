// pure_helpers_test.go — fill pkg/releaseinstall coverage of the
// tiny pure helpers and the wire-shape round-trips reachable
// without cosign, the Postgres schema, or an actual installed
// bundle on the host filesystem.
//
// Targets:
//   - ValidGitSHA (the canonical SHA shape predicate)
//   - FromManifest / ToManifest (the Bundle wire round-trip)
//   - SupportBinaryNames / RuntimeAssetNames (defensive copies)
//   - IsRuntimeAssetName / IsCatalogBinaryName / IsReleaseBinaryName
//
// Whitebox `package releaseinstall`.
package releaseinstall

import (
	"strings"
	"testing"
	"time"
)

// --- ValidGitSHA -----------------------------------------------

func TestValidGitSHA(t *testing.T) {
	cases := map[string]bool{
		"":                                   false, // empty
		strings.Repeat("a", 40):              true,  // full SHA-1
		strings.Repeat("A", 40):              false, // uppercase rejected
		"1234567":                            false, // too short
		strings.Repeat("g", 40):              false, // non-hex
		strings.Repeat("a", 39):              false, // off by one
		strings.Repeat("a", 41):              false, // off by one
		strings.Repeat("0", 40):              true,  // all-zeros is a valid SHA
		"deadbeef" + strings.Repeat("a", 32): true,  // mixed
	}
	for in, want := range cases {
		if got := ValidGitSHA(in); got != want {
			t.Errorf("ValidGitSHA(%q) = %v, want %v", in, got, want)
		}
	}
}

// --- FromManifest / ToManifest round-trip --------------------

// The wire shape (Bundle) and the internal shape (Manifest)
// carry identical field sets — FromManifest / ToManifest must
// be exact identity conversions in both directions.
func TestFromManifest_ToManifest_RoundTrip(t *testing.T) {
	createdAt := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	m := Manifest{
		FormatVersion: 1,
		GitSHA:        "abcdef1234567890abcdef1234567890abcdef12",
		ManifestHash:  "sha256:" + strings.Repeat("a", 64),
		DaemonHashes:  map[string]string{"apid": "sha256:" + strings.Repeat("b", 64)},
		ToolHashes:    map[string]string{"cosign": "sha256:" + strings.Repeat("c", 64)},
		AssetHashes:   map[string]string{"runners/go124/faas-runner": "sha256:" + strings.Repeat("d", 64)},
		CreatedAt:     createdAt,
		Signature:     "sig",
	}
	b := FromManifest(m)
	// Identity: every non-map field must match verbatim.
	if b.FormatVersion != m.FormatVersion ||
		b.GitSHA != m.GitSHA ||
		b.ManifestHash != m.ManifestHash ||
		b.Signature != m.Signature ||
		!b.CreatedAt.Equal(m.CreatedAt) {
		t.Errorf("FromManifest: got %+v, want %+v", b, m)
	}
	if len(b.DaemonHashes) != len(m.DaemonHashes) ||
		len(b.ToolHashes) != len(m.ToolHashes) ||
		len(b.AssetHashes) != len(m.AssetHashes) {
		t.Errorf("FromManifest: map lengths mismatch")
	}
	m2 := ToManifest(b)
	if m2.FormatVersion != m.FormatVersion ||
		m2.GitSHA != m.GitSHA ||
		m2.ManifestHash != m.ManifestHash ||
		m2.Signature != m.Signature ||
		!m2.CreatedAt.Equal(m.CreatedAt) {
		t.Errorf("ToManifest: got %+v, want %+v", m2, m)
	}
	if len(m2.DaemonHashes) != len(m.DaemonHashes) {
		t.Errorf("ToManifest: DaemonHashes length mismatch")
	}
}

// --- SupportBinaryNames / RuntimeAssetNames (defensive copy) --

func TestSupportBinaryNames_NonEmptyAndStable(t *testing.T) {
	got := SupportBinaryNames()
	if len(got) == 0 {
		t.Fatal("SupportBinaryNames empty")
	}
	want := []string{"gregale", "gregalectl", "init", "schedd-brokerq-apply", "vmmd-jail-helper", "vmmd-raw-bridge", "vmmd-stream-bridge", "vmlinux"}
	for i, name := range want {
		if got[i] != name {
			t.Errorf("[%d] = %q, want %q", i, got[i], name)
		}
	}
}

// Mutating the returned slice must NOT mutate the internal
// catalog.
func TestSupportBinaryNames_DefensiveCopy(t *testing.T) {
	got1 := SupportBinaryNames()
	got1[0] = "MUTATED"
	got2 := SupportBinaryNames()
	if got2[0] == "MUTATED" {
		t.Error("mutation leaked into internal catalog")
	}
}

func TestRuntimeAssetNames_NonEmptyAndStable(t *testing.T) {
	got := RuntimeAssetNames()
	if len(got) == 0 {
		t.Fatal("RuntimeAssetNames empty")
	}
	// All entries must contain "faas-runner" — the canonical
	// asset filename.
	for _, name := range got {
		if !strings.HasSuffix(name, "/faas-runner") {
			t.Errorf("%q does not end with /faas-runner", name)
		}
	}
}

// --- IsRuntimeAssetName --------------------------------------

func TestIsRuntimeAssetName(t *testing.T) {
	cases := map[string]bool{
		"":                                 false,
		"runners/go124/faas-runner":        true,
		"runners/python312/faas-runner":    true,
		"runners/not-a-runner/faas-runner": false, // unknown runtime
		"runners/node22/faas-runner-shim":  false, // not exactly the asset name
		"vmmd-raw-bridge":                  false, // a daemon, not a runner
	}
	for in, want := range cases {
		if got := IsRuntimeAssetName(in); got != want {
			t.Errorf("IsRuntimeAssetName(%q) = %v, want %v", in, got, want)
		}
	}
}

// --- IsCatalogBinaryName -------------------------------------

func TestIsCatalogBinaryName(t *testing.T) {
	cases := map[string]bool{
		"":                        false,
		"apid":                    true,  // logical name
		"vmmd":                    true,  // logical name
		"schedd":                  true,  // logical name
		"gatewayd-internal":       true,  // canonical gateway filename
		"gatewayd-internal-extra": false, // not a catalog name
		"some-other-tool":         false,
	}
	for in, want := range cases {
		if got := IsCatalogBinaryName(in); got != want {
			t.Errorf("IsCatalogBinaryName(%q) = %v, want %v", in, got, want)
		}
	}
}

// --- IsReleaseBinaryName --------------------------------------

func TestIsReleaseBinaryName(t *testing.T) {
	// Daemon (catalog) → true.
	if !IsReleaseBinaryName("apid") {
		t.Error("apid: got false, want true")
	}
	// Support binary → true.
	if !IsReleaseBinaryName("gregale") {
		t.Error("gregale: got false, want true")
	}
	// Neither → false.
	if IsReleaseBinaryName("some-random-tool") {
		t.Error("some-random-tool: got true, want false")
	}
}

// --- helpers --------------------------------------------------

// (no package-level test fixtures; see TestFromManifest_ToManifest_RoundTrip
// for the literal timestamp.)
