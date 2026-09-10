package imaged

// grype_test.go — behavior-parity pins for the PR-2 typed
// ScanResult refactor (issue #464 / ADR-055 + extension). The
// plan called for a behavior-parity assertion that the legacy
// base-ext4 sidecar JSON shape is byte-identical after the
// map[string]int → *ScanResult refactor. The round-trip
// through TestEnsureBaseExt4 already covers the integration
// path; this file pins the four small new primitives:
//
//   1. bumpSeverity: a normalised severity string increments
//      the right bucket; an unknown severity collapses to
//      UNKNOWN (the existing normalizeGrypeSeverity default).
//
//   2. toMap: a *ScanResult projects back into the
//      `map[string]int` shape the legacy sidecar JSON expects,
//      with the closed-enum keys (CRITICAL/HIGH/MEDIUM/LOW/
//      UNKNOWN) in the canonical order. nil receiver → nil
//      map (so the sidecar-write site can short-circuit on a
//      missing scan without panicking on a nil deref).
//
//   3. parseGrypeOutput: a grype JSON byte slice decodes into
//      the typed *ScanResult with id, package, version,
//      fixed_in, paths populated (the extension for
//      Vulnerability.Paths). Pin the artifact.locations[].path
//      flattening and the empty-locations / missing-fix cases.
//
//   4. vulnPaths helper: empty / nil locations → nil slice;
//      locations with empty paths filtered out. Pins the
//      jsonb omitempty-friendly contract.
//
// A failure here means a future refactor drifts the sidecar
// contract; vmmd's bringUpScanCheck would silently miss
// CRITICAL findings on the next staged base. A failure on
// (3) or (4) means a future change drops artifact fields
// at parse time and the dashboard's Path column renders
// "—" for every row.

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"testing"
)

// grypeSampleJSON is the fixture used by TestScanResult_ParseGrypeJSON.
//
//go:embed testdata/grype_sample.json
var grypeSampleJSON []byte

// grypeEmptyMatchesJSON covers the zero-finding path that
// must yield *ScanResult{} with Vulnerabilities == nil so the
// jsonb omitempty drops the field entirely.
const grypeEmptyMatchesJSON = `{"matches":[],"descriptor":{"name":"grype","version":"0.78.0"}}`

// TestScanResult_BumpSeverity pins the per-bucket increment
// against the closed-enum Severity* constants. The exact
// count for an unknown severity is UNKNOWN (matches the
// existing normalizeGrypeSeverity default at grype.go:118).
func TestScanResult_BumpSeverity(t *testing.T) {
	cases := []struct {
		severity string
		want     SeverityCounts
	}{
		{SeverityCritical, SeverityCounts{Critical: 1}},
		{SeverityHigh, SeverityCounts{High: 1}},
		{SeverityMedium, SeverityCounts{Medium: 1}},
		{SeverityLow, SeverityCounts{Low: 1}},
		{SeverityUnknown, SeverityCounts{Unknown: 1}},
		// Unknown / malformed severity collapses to UNKNOWN.
		{"", SeverityCounts{Unknown: 1}},
		{"bogus", SeverityCounts{Unknown: 1}},
		// Negligible is upper-cased by normalizeGrypeSeverity
		// before bumpSeverity is called, but a direct
		// bumpSeverity with "NEGLIGIBLE" still collapses to
		// UNKNOWN — the collapse happens at the
		// normalizeGrypeSeverity layer, not here. The pin is
		// for the direct call only.
		{"NEGLIGIBLE", SeverityCounts{Unknown: 1}},
	}
	for _, c := range cases {
		var got ScanResult
		got.bumpSeverity(c.severity)
		if got.SeverityCounts != c.want {
			t.Errorf("bumpSeverity(%q) = %+v, want %+v", c.severity, got.SeverityCounts, c.want)
		}
	}
}

// TestScanResult_ToMap pins the projection back to the
// legacy sidecar JSON shape. The keys MUST be the uppercase
// closed-enum Severity* constants so the sidecar JSON
// contract is byte-identical to the pre-PR-2 output for any
// given input. nil receiver → nil map (the sidecar-write
// site uses this to short-circuit on a missing scan).
func TestScanResult_ToMap(t *testing.T) {
	s := &ScanResult{
		SeverityCounts: SeverityCounts{
			Critical: 3, High: 2, Medium: 1, Low: 0, Unknown: 0,
		},
	}
	got := s.toMap()
	want := map[string]int{
		SeverityCritical: 3,
		SeverityHigh:     2,
		SeverityMedium:   1,
		SeverityLow:      0,
		SeverityUnknown:  0,
	}
	if len(got) != len(want) {
		t.Errorf("toMap returned %d keys, want %d", len(got), len(want))
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("toMap[%q] = %d, want %d", k, got[k], v)
		}
	}
}

// TestScanResult_ToMap_NilReceiver pins the nil-safe short
// circuit. The sidecar-write site at base_stage.go reads
// `findings.toMap()` after a nil-check; a nil receiver that
// panics would surface as a build-pipeline crash instead of
// the expected fail-closed CRITICAL=9999 placeholder path.
func TestScanResult_ToMap_NilReceiver(t *testing.T) {
	var s *ScanResult
	if got := s.toMap(); got != nil {
		t.Errorf("nil receiver: toMap() = %v, want nil", got)
	}
}

// TestScanResult_ParseGrypeJSON pins the typed parser for the
// per-deploy surface. The fixture (testdata/grype_sample.json)
// is hand-authored and contains three matches:
//
//   - CRITICAL with two locations (libssl + libcrypto).
//   - HIGH with no locations (deb/apt-style CVE match).
//   - MEDIUM with a missing-locations field and empty fix.versions
//     (no available patch).
//
// A regression that drops artifact.locations[].path from the
// parse step surfaces here as Paths == nil on the CRITICAL
// match. A regression that fails to normalise severity surfaces
// as Critical: 0 (the un-normalised "Critical" string fails to
// match the bucket switch in bumpSeverity).
func TestScanResult_ParseGrypeJSON(t *testing.T) {
	// Defensive check that the embed actually populated the
	// fixture. A regression in the //go:embed directive would
	// otherwise panic with `go:embed: no matching files`.
	if len(grypeSampleJSON) == 0 {
		t.Fatalf("embedded grype_sample.json is empty")
	}

	res, err := parseGrypeOutput(grypeSampleJSON, "/srv/fc/jail/test/rootfs")
	if err != nil {
		t.Fatalf("parseGrypeOutput: %v", err)
	}
	if res == nil {
		t.Fatal("parseGrypeOutput returned nil without error")
	}

	// Per-bucket counts from the fixture: 1 CRITICAL, 1 HIGH,
	// 1 MEDIUM. The MEDIUM match reads "Medium" (not "MEDIUM")
	// in the upstream grype output; the bumpSeverity call
	// routes via normalizeGrypeSeverity which upper-cases to
	// MEDIUM — so the SeverityCounts.Medium bucket is 1.
	want := SeverityCounts{Critical: 1, High: 1, Medium: 1, Low: 0, Unknown: 0}
	if res.SeverityCounts != want {
		t.Errorf("SeverityCounts = %+v, want %+v", res.SeverityCounts, want)
	}

	// Vulnerability list pins for issue #464 / extension
	// (Vulnerability.Paths):
	if got := len(res.Vulnerabilities); got != 3 {
		t.Fatalf("len(Vulnerabilities) = %d, want 3", got)
	}
	v0 := res.Vulnerabilities[0]
	if v0.ID != "CVE-2024-1234" {
		t.Errorf("Vulns[0].ID = %q, want CVE-2024-1234", v0.ID)
	}
	if v0.Severity != "CRITICAL" {
		t.Errorf("Vulns[0].Severity = %q, want CRITICAL (normalizeGrypeSeverity path)", v0.Severity)
	}
	if v0.Package != "openssl" {
		t.Errorf("Vulns[0].Package = %q, want openssl", v0.Package)
	}
	if v0.Version != "1.1.1k-7" {
		t.Errorf("Vulns[0].Version = %q, want 1.1.1k-7", v0.Version)
	}
	if v0.FixedIn != "1.1.1l-1" {
		t.Errorf("Vulns[0].FixedIn = %q, want 1.1.1l-1 (fix.versions[0])", v0.FixedIn)
	}
	if len(v0.Paths) != 2 {
		t.Fatalf("Vulns[0].Paths has %d entries, want 2", len(v0.Paths))
	}
	if v0.Paths[0] != "/usr/lib/x86_64-linux-gnu/libssl.so.1.1" {
		t.Errorf("Vulns[0].Paths[0] = %q, want libssl path", v0.Paths[0])
	}
	if v0.Paths[1] != "/usr/lib/x86_64-linux-gnu/libcrypto.so.1.1" {
		t.Errorf("Vulns[0].Paths[1] = %q, want libcrypto path", v0.Paths[1])
	}

	// High match: empty locations slice → Paths nil, NOT a
	// 0-length but non-nil slice (the jsonb omitempty contract
	// requires nil-on-empty).
	v1 := res.Vulnerabilities[1]
	if v1.ID != "CVE-2024-5678" {
		t.Errorf("Vulns[1].ID = %q, want CVE-2024-5678", v1.ID)
	}
	if v1.Severity != "HIGH" {
		t.Errorf("Vulns[1].Severity = %q, want HIGH", v1.Severity)
	}
	if v1.Paths != nil {
		t.Errorf("Vulns[1].Paths = %v, want nil (empty locations → nil, omitempty)", v1.Paths)
	}
	if v1.FixedIn != "2.4.55-2" {
		t.Errorf("Vulns[1].FixedIn = %q, want 2.4.55-2", v1.FixedIn)
	}

	// Medium match: missing locations field entirely
	// (artifact block has only name + version) → Paths nil.
	// Empty fix.versions → FixedIn "" (no patch).
	v2 := res.Vulnerabilities[2]
	if v2.ID != "GHSA-9999-aaaa-bbbb" {
		t.Errorf("Vulns[2].ID = %q, want GHSA-9999-aaaa-bbbb", v2.ID)
	}
	if v2.Severity != "MEDIUM" {
		t.Errorf("Vulns[2].Severity = %q, want MEDIUM", v2.Severity)
	}
	if v2.Paths != nil {
		t.Errorf("Vulns[2].Paths = %v, want nil (missing locations → nil)", v2.Paths)
	}
	if v2.FixedIn != "" {
		t.Errorf("Vulns[2].FixedIn = %q, want empty (no fix available)", v2.FixedIn)
	}
}

// TestParseGrypeOutput_EmptyMatches pins the zero-finding
// contract: a `matches: []` payload returns
// (*ScanResult{Vulnerabilities: []Vulnerability{}}, nil)
// — the Vulnerabilities slice MUST be non-nil but zero-
// length so the wire JSON emits "vulnerabilities":[] (not
// "vulnerabilities":null). The OpenAPI schema at
// api/openapi.yaml:5590 declares the array type without
// nullable: true; a strict OpenAPI 3.1 client validator
// rejects null. The marshal-output sub-assertion below
// pins the wire contract — a regression that returns
// (*ScanResult{}, nil) (Vulnerabilities == nil) would emit
// "vulnerabilities":null and fail the marshal check.
//
// The dashboard's "No vulnerabilities matched." copy still
// renders cleanly because Go templates treat both nil and
// 0-length slices as falsy.
func TestParseGrypeOutput_EmptyMatches(t *testing.T) {
	res, err := parseGrypeOutput([]byte(grypeEmptyMatchesJSON), "/srv/fc/jail/empty/rootfs")
	if err != nil {
		t.Fatalf("parseGrypeOutput: %v", err)
	}
	if res == nil {
		t.Fatal("parseGrypeOutput returned nil without error")
	}
	if res.Vulnerabilities == nil {
		t.Error("Vulnerabilities = nil; want []Vulnerability{} (non-nil empty slice — wire must emit \"vulnerabilities\":[] not null)")
	}
	if len(res.Vulnerabilities) != 0 {
		t.Errorf("len(Vulnerabilities) = %d, want 0", len(res.Vulnerabilities))
	}
	if res.SeverityCounts != (SeverityCounts{}) {
		t.Errorf("SeverityCounts = %+v, want zero value", res.SeverityCounts)
	}

	// Wire-contract pin: marshal the result and assert the
	// JSON contains "vulnerabilities":[] (NOT null). This is
	// the load-bearing assertion — the OpenAPI schema and
	// strict 3.1 client validators depend on it.
	blob, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal ScanResult: %v", err)
	}
	if !bytes.Contains(blob, []byte(`"vulnerabilities":[]`)) {
		t.Errorf("wire JSON missing \"vulnerabilities\":[]; got: %s", string(blob))
	}
	if bytes.Contains(blob, []byte(`"vulnerabilities":null`)) {
		t.Errorf("wire JSON emitted \"vulnerabilities\":null (must be [] for OpenAPI 3.1 strict clients); got: %s", string(blob))
	}
	if !bytes.Contains(blob, []byte(`"severity_counts":{"critical":0,"high":0,"medium":0,"low":0,"unknown":0}`)) {
		t.Errorf("wire JSON missing canonical nested severity_counts; got: %s", string(blob))
	}
}

// TestParseGrypeOutput_MalformedJSON pins the fail-closed
// parse error path: a JSON blob that doesn't decode returns
// (nil, err). The caller (runGrypeImpl → writeScanSidecar)
// treats both as a scan-failed path so vmmd refuses to boot
// an un-scanned base. A regression that returns
// (*ScanResult{}, nil) on a malformed input would silently
// advertise a clean scan on an unscannable image.
func TestParseGrypeOutput_MalformedJSON(t *testing.T) {
	_, err := parseGrypeOutput([]byte("{not-valid-json"), "/srv/fc/jail/abc")
	if err == nil {
		t.Error("parseGrypeOutput: want error on malformed JSON, got nil")
	}
}

// TestVulnPaths pins the artifact.locations[].path flattening
// helper used by parseGrypeOutput. The empty / nil contract
// matters because the jsonb omitempty drop depends on the
// return being nil (a 0-length but non-nil slice would still
// emit `"paths":[]` on the wire).
func TestVulnPaths(t *testing.T) {
	cases := []struct {
		name string
		in   []grypeLocation
		want []string
	}{
		{name: "nil", in: nil, want: nil},
		{name: "empty", in: []grypeLocation{}, want: nil},
		{name: "single", in: []grypeLocation{{Path: "/usr/lib/a.so"}}, want: []string{"/usr/lib/a.so"}},
		{name: "multi", in: []grypeLocation{{Path: "/a"}, {Path: "/b"}}, want: []string{"/a", "/b"}},
		// Empty path entries are filtered (Grype never emits
		// them but a future schema or hand-authored fixture
		// could):
		{name: "filters-empty", in: []grypeLocation{{Path: ""}, {Path: "/keep"}}, want: []string{"/keep"}},
		{name: "all-empty", in: []grypeLocation{{Path: ""}, {Path: ""}}, want: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := vulnPaths(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("vulnPaths len = %d, want %d (got=%v want=%v)", len(got), len(tc.want), got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("vulnPaths[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}
