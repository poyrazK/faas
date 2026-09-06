package imaged

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// grype.go — Grype subprocess runner (issue #299).
//
// The default Grype runner shells out to the grype CLI, parses the
// JSON output, and returns per-severity finding counts as
// map[string]int. The runner is fail-soft at the parse layer
// (returns (nil, err) when the JSON is malformed), and the sidecar
// write site (pkg/imaged/base_stage.go) treats a nil-map return as
// a CRITICAL=9999 placeholder so vmmd refuses to boot any un-scanned
// base ext4 — fail-closed by design.
//
// Grype's JSON schema is the public one documented at
// https://github.com/anchore/grype (output format "json" emits a
// top-level `matches` array; each match carries
// `vulnerability.severity` ∈ {Negligible, Low, Medium, High,
// Critical, Unknown}). We lowercase the Grype severity to match
// the counter convention used elsewhere in the repo
// (CRITICAL/HIGH/MEDIUM/LOW/UNKNOWN, uppercase).

// grypeMatch is the slim subset of the Grype JSON output we
// consume. The full schema is documented at
// https://github.com/anchore/grype/blob/main/schema/json/schema-9.0.json
// (issue #464 / extension): we now decode vulnerability.{id,fix.versions[0]}
// and artifact.{name,version,locations[].path} in addition to severity,
// so the typed Vulnerability in the jsonb payload (PR-3 sink) carries
// the data the dashboard's per-row "Path" column renders. The
// fail-closed base-ext4 sidecar (writeScanSidecar) only reads
// vulnerability.severity, so the existing base-factory scan contract
// is semantically equivalent for any given input — the new fields
// flow through ScanResult.Vulnerabilities for the per-deploy
// surface only. (The legacy sidecar JSON was never byte-identical
// across runs anyway: writeScanSidecar marshals a map[string]int
// and Go's map iteration order is randomised; the consumer
// reads via json.Unmarshal into a map and never inspects key
// order.)
type grypeMatch struct {
	Vulnerability struct {
		ID       string `json:"id"`
		Severity string `json:"severity"`
		Fix      struct {
			Versions []string `json:"versions"`
		} `json:"fix"`
	} `json:"vulnerability"`
	Artifact struct {
		Name      string          `json:"name"`
		Version   string          `json:"version"`
		Locations []grypeLocation `json:"locations"`
	} `json:"artifact"`
}

// grypeLocation is one element of artifact.locations — the per-file
// path within the scanned dir: Grype groups multiple files for the
// same vuln into one match, so a single CVE can have several paths.
// Hand-authored fixtures in testdata/ pin this shape (see
// TestScanResult_ParseGrypeJSON).
type grypeLocation struct {
	Path string `json:"path"`
}

// grypeOutput is the top-level shape of `grype dir:<dir> -o json`.
// The `matches` array carries one entry per detected vulnerability;
// descriptor fields (ignored here) include the source artifact and
// Grype's database version.
type grypeOutput struct {
	Matches []grypeMatch `json:"matches"`
}

// defaultGrypeRun shells out to the grype CLI and parses the JSON
// output into a typed ScanResult (issue #299 / ADR-055 / PR-2). ctx
// cancellation propagates to the subprocess via exec.CommandContext
// (same pattern as pkg/fcvm/metrics.go:349's lvs invocation).
// Returns (nil, err) on a subprocess error or parse failure —
// the caller (EnsureBaseExt4's sidecar write) treats both as a
// scan-failed path and writes the fail-closed CRITICAL=9999
// placeholder.
//
// The binary is resolved via exec.LookPath (no absolute path
// override); the imaged ansible role is expected to install grype
// at a default-PATH location. A missing grype binary surfaces
// here as a `exec: "grype": executable file not found in $PATH`
// error from the subprocess — same fail-closed path.
func defaultGrypeRun(ctx context.Context, dir string) (*ScanResult, error) {
	return runGrypeImpl(ctx, "grype", dir)
}

// RunGrype is the production entry point for cmd/imaged when the
// operator pins grype to PATH (FAAS_GRYPE_BIN is empty). It is the
// same code path defaultGrypeRun uses, exported so cmd/imaged can
// hand a single function value to WithGrypeRun without a closure
// wrapper. Pinned by TestRunGrype_DelegatesToSubprocess in
// grype_test.go (run with FAAS_RUN_GRYPE_TESTS=1).
func RunGrype(ctx context.Context, dir string) (*ScanResult, error) {
	return defaultGrypeRun(ctx, dir)
}

// RunGrypeAt is the operator-pinned variant: when the ansible role
// installs grype at a non-PATH location (e.g. /opt/grype/bin/grype),
// cmd/imaged passes that path via FAAS_GRYPE_BIN and the closure
// inside makeGrypeRunner binds the binary to RunGrypeAt so the
// subprocess invocation doesn't depend on $PATH resolution.
func RunGrypeAt(ctx context.Context, bin, dir string) (*ScanResult, error) {
	return runGrypeImpl(ctx, bin, dir)
}

// runGrypeImpl is the shared body. Parameterised on the binary
// path so defaultGrypeRun (PATH lookup, "grype") and RunGrypeAt
// (operator-supplied absolute path) share the same parse +
// counting logic; the per-call dispatch is one switch.
//
// PR-2 (issue #464 / ADR-055): the return type changed from
// `map[string]int` to `*ScanResult`. The SeverityCounts field
// carries the same per-bucket count the pre-PR-2 map did; the
// full Vulnerability[] list is the typed payload the per-deploy
// sink (runDeployScan → state.Store.UpsertDeploymentScanResult)
// writes to deployments.scan_result jsonb.
//
// Extension (issue #464 / PR-B acceptance): runGrypeImpl now
// populates ScanResult.Vulnerabilities with id, package, version,
// fixed_in, and paths (artifact.locations[].path) per match.
// The base-ext4 sidecar (writeScanSidecar) reads only
// SeverityCounts; the new fields flow through ScanResult and
// are written to the deployment row, never to the sidecar.
// scanResponse decodes them back into api.Vulnerability — the
// /scan route and DeploymentResponse.Scan carry the full list;
// the dashboard's handler-edge cap (cmd/apid/handlers_dashboard.go)
// truncates to 10 before the template renders.
func runGrypeImpl(ctx context.Context, bin, dir string) (*ScanResult, error) {
	scanDir, cleanup, err := prepareGrypeSource(ctx, dir)
	if err != nil {
		return nil, fmt.Errorf("imaged: prepare grype source %q: %w", dir, err)
	}
	defer cleanup()
	cmd := exec.CommandContext(ctx, bin, "dir:"+scanDir, "-o", "json")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("imaged: grype scan dir %q: %w (stderr=%q)", dir, err, stderr.String())
	}
	return parseGrypeOutput(stdout.Bytes(), dir)
}

// prepareGrypeSource turns an ext4 image into a directory Grype can catalog.
// Grype's dir source walks a filesystem tree; it does not mount or unpack an
// ext4 file passed as dir:<path>. Base staging produces ext4 images, and the
// remote per-deploy scan stages one as rootfs.ext4 inside a temporary folder,
// so both shapes are normalized here before invoking the scanner.
//
// debugfs only reads the image. The extracted tree is temporary and is
// removed by the returned cleanup function. The parent directory is selected
// from the input path so OCI scan materialization stays under the daemon's
// already-authorized scratch root rather than falling back to an arbitrary
// system temp location.
func prepareGrypeSource(ctx context.Context, source string) (string, func(), error) {
	info, err := os.Stat(source)
	if err != nil {
		return "", func() {}, err
	}
	image := source
	root := filepath.Dir(source)
	if info.IsDir() {
		candidate := filepath.Join(source, "rootfs.ext4")
		candidateInfo, statErr := os.Stat(candidate)
		if statErr != nil {
			if os.IsNotExist(statErr) {
				return source, func() {}, nil
			}
			return "", func() {}, fmt.Errorf("stat rootfs image: %w", statErr)
		}
		if candidateInfo.IsDir() {
			return source, func() {}, nil
		}
		image = candidate
		root = filepath.Dir(source)
	}

	stageDir, err := os.MkdirTemp(root, "imaged-grype-")
	if err != nil {
		return "", func() {}, fmt.Errorf("mkdir extraction dir: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(stageDir) }
	request := fmt.Sprintf("rdump / %s", stageDir)
	cmd := exec.CommandContext(ctx, "debugfs", "-R", request, image)
	if output, err := cmd.CombinedOutput(); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("debugfs extract: %w (output=%q)", err, string(output))
	}
	// debugfs preserves image ownership and modes. The scan runs as the
	// unprivileged imaged user in the canonical unit, so make the temporary
	// copy readable/traversable without changing the source image.
	if output, err := exec.CommandContext(ctx, "chmod", "-R", "a+rX", stageDir).CombinedOutput(); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("chmod extraction: %w (output=%q)", err, string(output))
	}
	return stageDir, cleanup, nil
}

// parseGrypeOutput decodes the grype JSON output into a typed
// *ScanResult. Extracted from runGrypeImpl so unit tests can
// exercise the parser without a grype subprocess or a real image
// on disk. The dir argument is only used to format the parse
// error message — the bytes are the entire grype JSON; the dir
// does not appear in the result.
func parseGrypeOutput(raw []byte, dir string) (*ScanResult, error) {
	var out grypeOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("imaged: grype scan dir %q: parse json: %w", dir, err)
	}
	if len(out.Matches) == 0 {
		// Zero-finding scan: return *ScanResult with an
		// empty (NOT nil) Vulnerabilities slice so the
		// wire JSON emits "vulnerabilities":[] (not
		// null). The OpenAPI schema at
		// api/openapi.yaml:5590 declares the array type
		// without nullable: true; a strict OpenAPI 3.1
		// client validator rejects null. The base-ext4
		// sidecar (writeScanSidecar) reads only
		// SeverityCounts which is zero-valued either way.
		return &ScanResult{Vulnerabilities: []Vulnerability{}}, nil
	}
	res := &ScanResult{Vulnerabilities: make([]Vulnerability, 0, len(out.Matches))}
	for _, m := range out.Matches {
		res.bumpSeverity(normalizeGrypeSeverity(m.Vulnerability.Severity))
		res.Vulnerabilities = append(res.Vulnerabilities, Vulnerability{
			ID:       m.Vulnerability.ID,
			Severity: normalizeGrypeSeverity(m.Vulnerability.Severity),
			Package:  m.Artifact.Name,
			Version:  m.Artifact.Version,
			FixedIn:  vulnFixedIn(m.Vulnerability.Fix.Versions),
			Paths:    vulnPaths(m.Artifact.Locations),
		})
	}
	return res, nil
}

// vulnFixedIn picks the first fix version Grype reports. Future
// extension: persist all of fix.versions[] in a follow-up if a CVE
// carries multiple fixed versions (rare in practice — Grype usually
// emits one fixed-version string per CVE).
func vulnFixedIn(versions []string) string {
	if len(versions) == 0 {
		return ""
	}
	return versions[0]
}

// vulnPaths flattens artifact.locations[].path into a single slice.
// Many CVEs are reported against a package without a file path
// (deb/apt-style CVE matches); the nil/empty case returns nil so the
// jsonb omitempty drops the field on the wire.
func vulnPaths(locs []grypeLocation) []string {
	if len(locs) == 0 {
		return nil
	}
	out := make([]string, 0, len(locs))
	for _, l := range locs {
		if l.Path != "" {
			out = append(out, l.Path)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ScanResult is the typed result of one grype run (issue #464 /
// ADR-055 / PR-2 + extension). The pre-PR-2 call sites returned
// `map[string]int`; the new struct carries the per-bucket count
// (SeverityCounts) and the full Vulnerability[] list that
// runGrypeImpl / parseGrypeOutput populate.
//
// The package-level `Severity` constants below are the closed
// enum Grype normalises to (CRITICAL|HIGH|MEDIUM|LOW|UNKNOWN).
// The base-ext4 sidecar write at base_stage.go::writeScanSidecar
// reads SeverityCounts.Critical / .High / .Medium / .Low / .Unknown
// to build the legacy `findings map[string]int` payload — the
// pre-PR-2 sidecar JSON shape is semantically equivalent for a
// given input (same key set + values). The per-deploy surface
// (PR-3's deploy-complete hook in handler.go::runDeployScan →
// state.Store) marshals the full struct into the
// deployments.scan_result jsonb; the wire DTO (api.ScanResult)
// decodes both fields back. The dashboard's "top 10 by severity"
// view is a handler-edge cap (per-extend); the wire keeps the
// full list.
//
// Error carries the grype-runner error message on the
// scan_status='failed' path (PR-3 retry-exhausted backoff).
// Empty on success. Marshalled into deployments.scan_result
// jsonb so the dashboard's "scan failed" chip can render the
// underlying cause; not surfaced on the success path.
//
// Vulnerabilities carries the full CVE list in Grype's natural
// output order (most-severe-first in the upstream JSON).
// ALWAYS present on the wire (no omitempty) — parseGrypeOutput
// initialises the slice to []Vulnerability{} for zero-finding
// scans so the marshal output is "vulnerabilities":[] (not
// null). The OpenAPI schema at api/openapi.yaml:5590 declares
// the array type without nullable: true; a strict OpenAPI 3.1
// client validator rejects null. Empty-slice vs nil is the
// distinction the wire contract relies on.
type ScanResult struct {
	SeverityCounts
	// Vulnerabilities is the full typed CVE list, ALWAYS
	// present (no omitempty). For a zero-finding scan
	// (len(out.Matches) == 0 in parseGrypeOutput) the
	// slice is []Vulnerability{} so the wire JSON
	// emits "vulnerabilities":[] — never null. See
	// api/openapi.yaml:5590 (no nullable: true) and the
	// TestParseGrypeOutput_EmptyMatches pin.
	Vulnerabilities []Vulnerability `json:"vulnerabilities"`
	// Error is the grype-runner error message stamped on the
	// scan_status='failed' path (PR-3 retry-exhausted backoff).
	// Empty on success. Marshalled with omitempty so a successful
	// scan's jsonb payload doesn't carry the field.
	Error string `json:"error,omitempty"`
}

// bumpSeverity increments the per-bucket count for one
// normalised severity. A future severity (or an empty string
// from a malformed match) maps to UNKNOWN so the count still
// records an honest value rather than dropping the row.
func (s *ScanResult) bumpSeverity(severity string) {
	switch severity {
	case SeverityCritical:
		s.Critical++
	case SeverityHigh:
		s.High++
	case SeverityMedium:
		s.Medium++
	case SeverityLow:
		s.Low++
	case SeverityUnknown:
		s.Unknown++
	default:
		s.Unknown++
	}
}

// toMap projects the typed SeverityCounts back into the
// `map[string]int` shape the legacy base-ext4 scan sidecar
// expects (consumed by vmmd's bringUpScanCheck at
// pkg/fcvm/manager.go). The keys are the uppercase closed
// enum (CRITICAL/HIGH/MEDIUM/LOW/UNKNOWN). The map shape is
// semantically equivalent to the pre-PR-2 output for any given
// input (same key set + values) so the sidecar contract is
// preserved; the marshal output is NOT byte-identical across
// runs because Go's map iteration order is randomised — the
// consumer reads via json.Unmarshal into a map and never
// inspects key order.
func (s *ScanResult) toMap() map[string]int {
	if s == nil {
		return nil
	}
	return map[string]int{
		SeverityCritical: s.Critical,
		SeverityHigh:     s.High,
		SeverityMedium:   s.Medium,
		SeverityLow:      s.Low,
		SeverityUnknown:  s.Unknown,
	}
}

// fixAvailableToMap returns the severity counts for vulnerabilities that have
// an upstream fix. Runtime-base publication applies the same policy with
// Grype's --only-fixed flag: unfixed vendor findings remain visible, while
// findings an operator can remediate block admission.
func (s *ScanResult) fixAvailableToMap() map[string]int {
	if s == nil {
		return nil
	}
	fixed := &ScanResult{}
	for _, vulnerability := range s.Vulnerabilities {
		if vulnerability.FixedIn == "" {
			continue
		}
		fixed.bumpSeverity(vulnerability.Severity)
	}
	return fixed.toMap()
}

// SeverityCounts is the per-bucket count of CVEs in Grype's
// closed vocabulary. Mirrors pkg/api.SeverityCounts (the
// customer-facing DTO) but lives in imaged to keep the
// internal type dependency-free. PR-3's sink marshals this
// struct directly into the deployments.scan_result jsonb
// column.
type SeverityCounts struct {
	Critical int
	High     int
	Medium   int
	Low      int
	Unknown  int
}

// Severity closed enum (issue #299 / ADR-055). Grype emits
// a leading uppercase letter (Critical, High, Medium, Low,
// Negligible, Unknown); the in-package constant set is
// upper-case to match the pkg/api closed enum and the
// existing vmmd scan sidecar. Negligible collapses into LOW
// (the existing normalizeGrypeSeverity convention).
const (
	SeverityCritical = "CRITICAL"
	SeverityHigh     = "HIGH"
	SeverityMedium   = "MEDIUM"
	SeverityLow      = "LOW"
	SeverityUnknown  = "UNKNOWN"
)

// normalizeGrypeSeverity upper-cases Grype's severity strings to
// match the canonical closed set used by vmmd's scan sidecar
// (CRITICAL/HIGH/MEDIUM/LOW/UNKNOWN). Grype emits a leading
// uppercase letter (Critical, High, Medium, Low, Negligible,
// Unknown); the public Grype docs document this vocabulary. A
// future severity (or an empty string from a malformed match)
// collapses to UNKNOWN so the counter still records an honest
// count rather than dropping the row.
func normalizeGrypeSeverity(s string) string {
	switch s {
	case "Critical":
		return "CRITICAL"
	case "High":
		return "HIGH"
	case "Medium":
		return "MEDIUM"
	case "Low":
		return "LOW"
	case "Negligible":
		// Grype's "Negligible" severity is mapped to LOW
		// here so the dashboard's LOW row absorbs it; the
		// §12 dashboard panels do not separately chart
		// Negligible. The two-row collapse is documented at
		// memory note `grype-severity-mapping`.
		return "LOW"
	case "Unknown":
		return "UNKNOWN"
	default:
		return "UNKNOWN"
	}
}
