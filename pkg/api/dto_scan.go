package api

// dto_scan.go — per-deploy grype CVE scan DTOs (issue #464 /
// ADR-055 / PR-1 data plane).
//
// These types are the wire shape that apid's `getDeployment`
// (additive DeploymentResponse.Scan field, PR-3) and
// `getDeploymentScan` (new /scan route, PR-4) emit. The
// Dashboard and CLI read the same DTOs. The on-disk shape is
// `deployments.scan_result jsonb` (migration 00135); the
// apid-side `SerializeScanResult` helper (in
// pkg/api/state/serialize.go) marshals the jsonb column into
// the DTO; the PR-3 sink writes the jsonb column on the
// deploy-complete path.
//
// Field naming follows the issue's spec verbatim — the dashboard
// renders the JSON keys directly. `omitempty` is set on every
// field that can be absent so the on-the-wire shape is the
// minimum useful payload. The PR-1 migration's
// `scan_status='skipped'` sentinel is reflected in
// Scan.Status (see below).

// ScanResult is the customer-facing wire shape of one
// deployment's grype scan (issue #464 / ADR-075, PR-1).
// `Status` is the closed enum that mirrors the DB's
// `deployments.scan_status` column. The `omitempty` rules:
//
//   - ScannedAt: zero on rows that haven't been scanned yet
//     (Status = "pending" or "skipped" or NULL).
//   - ImageDigest: empty only on the pre-feature backfill rows
//     (Status = "skipped" with no image-digest to stamp).
//   - ScannerVersion: empty on a deployment whose scan ran
//     against a removed grype binary; in practice every scan
//     in production has a non-empty version.
//
// Vulnerabilities is the array of CVEs the grype run matched.
// The dashboard's "top 10 by severity" view sorts this array
// in-memory; the wire shape is the full list. The /scan route
// (PR-4) returns this struct unchanged.
//
// SeverityCounts mirrors the (CRITICAL|HIGH|MEDIUM|LOW|UNKNOWN)
// Grype closed set normalized by normalizeGrypeSeverity. Empty
// on rows with Status="skipped" (no scan ran) or Status="failed"
// (the scan errored, the count is meaningless).
type ScanResult struct {
	// Status mirrors the deployments.scan_status column
	// (closed enum pending|complete|failed|skipped; the
	// pre-feature backfill stamps 'skipped' on every row that
	// predates migration 00135). Always present so the
	// dashboard can render a uniform "scan pending" / "scan
	// failed" / "scan skipped" pill without a missing-key
	// check.
	Status string `json:"status"`
	// ScannedAt is the wall clock the grype run completed
	// (RFC 3339 UTC). Empty when Status != "complete".
	// Distinct from deployments.created_at — the deploy
	// ships before the scan lands (AC #1: scan within
	// 5 min of status: live, not at the same instant).
	ScannedAt string `json:"scanned_at,omitempty"`
	// ScannerVersion is the grype binary version that
	// produced the scan (e.g. "0.78.0"), copied from the
	// Grype result descriptor.
	ScannerVersion string `json:"scanner_version,omitempty"`
	// ArtifactDigest is the SHA-256 of the exact ext4 artifact handed to
	// Grype. It binds the evidence to the bytes selected for scanning.
	ArtifactDigest string `json:"artifact_digest,omitempty"`
	// ScannerDBVersion and ScannerDBBuiltAt identify the vulnerability
	// database used by Grype. Enforce mode rejects evidence without a
	// usable database identity or outside the freshness window.
	ScannerDBVersion string `json:"scanner_db_version,omitempty"`
	ScannerDBBuiltAt string `json:"scanner_db_built_at,omitempty"`
	// ImageDigest is the deployment's OCI image digest at
	// the time of the scan. Sourced from
	// deployments.image_digest, not re-inspected. Empty on
	// the pre-feature backfill (Status = "skipped" with no
	// image to stamp).
	ImageDigest string `json:"image_digest,omitempty"`
	// ScannerDBStatus is the status Grype reported for its vulnerability
	// database. It is omitted on legacy rows without descriptor metadata.
	ScannerDBStatus string `json:"scanner_db_status,omitempty"`
	// SeverityCounts is the per-bucket count of CVEs
	// Grype matched. Always present (zero-value map
	// serializes as `{}`); the dashboard reads
	// counts.Critical / .High / .Medium / .Low / .Unknown.
	SeverityCounts SeverityCounts `json:"severity_counts"`
	// Vulnerabilities is the full CVE list, ordered by
	// Grype's natural output (most-severe-first in the
	// upstream JSON). The dashboard's "top 10" view
	// sorts+truncates this client-side. The /scan route
	// returns the full list.
	//
	// ALWAYS present (no omitempty). For a zero-finding
	// scan the slice is empty but non-nil so the wire
	// JSON emits "vulnerabilities":[] (never null). The
	// OpenAPI schema at api/openapi.yaml:5590 declares
	// the array type without nullable: true; a strict
	// OpenAPI 3.1 client validator rejects null. PR #656
	// review finding #1 closed this gap by initialising
	// the slice in parseGrypeOutput.
	Vulnerabilities []Vulnerability `json:"vulnerabilities"`
	// Error is the grype runner's last error message on a
	// failed scan (Status = "failed"). Empty on every
	// other status. The PR-3 sink captures the message
	// after the 1-retry backoff is exhausted.
	Error string `json:"error,omitempty"`
}

// SecretScanResult is the customer-facing wire shape of one
// deployment's secret-scan audit row (migrations/00221,
// secret-scan v2). Mirrors the ScanResult shape above but for
// the server-side secret pipeline (cmd/apid/secretscan.go) —
// the two never overlap because they stamp different columns on
// the deployments row (scan_result + scanned_at for grype;
// secret_findings + secret_scanned_at for secrets).
//
// Status is the closed enum that mirrors deployments.scan_status
// for the secret scan: the same five values (pending, complete,
// failed, skipped, complete_with_redactions) but only the
// last is set by the secret-scan pipeline — the secret-scan
// either found redactions needed (complete_with_redactions) or
// it didn't run yet (pending). The grype pipeline writes the
// other three values; the two pipelines therefore touch the
// same status column without colliding because they fire at
// different lifecycle phases (secret scan: pre-apply, grype:
// post-build).
//
// Findings is always non-nil (empty slice on a clean deploy) so
// the wire JSON emits "findings":[] (never null). The
// per-finding Snippet field is the pre-truncated safe
// representation (first 6 + "…" + last 4) — never the raw
// value, matching the snippet policy documented in
// pkg/secretscan/scan.go.
type SecretScanResult struct {
	Status    string          `json:"status"`
	ScannedAt string          `json:"scanned_at,omitempty"`
	Findings  []SecretFinding `json:"findings"`
	// ImageDigest is the OCI digest the layered scan was run
	// against (PR-A). Mirrors ScanResult.ImageDigest (Grype
	// path) so a customer comparing scan + secret-scan rows
	// for the same deploy can see both scans ran against the
	// same bytes. Omitted from the apid source-tree
	// rejection payload (the cmd/apid 422 path doesn't have
	// an image digest — the customer hasn't uploaded yet).
	ImageDigest string `json:"image_digest,omitempty"`
	// Error carries an in-band explanation when the audit
	// row is unreadable (jsonb decode failed, server logs
	// carry the detail). Mirrors ScanResult.Error so the
	// dashboard's "scan summary unavailable" pill has
	// something to render for a malformed row. Omitted on
	// the success path (Status carries the closed-set
	// signal: "complete" | "complete_with_redactions").
	Error string `json:"error,omitempty"`
}

// SeverityCounts is the per-severity bucket count of CVEs
// (issue #464 / ADR-075). The shape mirrors Grype's closed
// vocabulary; the in-repo normalizeGrypeSeverity collapses
// "Negligible" into LOW so the dashboard's "LOW" bucket
// absorbs both (the Negligible split is omitted — there is
// no customer value in a separate bucket for severity the
// upstream itself flags as negligible).
//
// All fields are present (no `omitempty`) so the JSON shape
// is uniform across rows. A bucket with zero findings
// serializes as `"critical": 0` — the dashboard reads
// counts without nil-checks.
type SeverityCounts struct {
	Critical int `json:"critical"`
	High     int `json:"high"`
	Medium   int `json:"medium"`
	Low      int `json:"low"`
	Unknown  int `json:"unknown"`
}

// Vulnerability is one row in the scan's CVE list
// (issue #464 / ADR-075, PR-1 + extension).
//
// FixedIn is the upstream's "fixed in" version string.
// Empty when Grype reports no fix is available (e.g. a
// disputed CVE) — the dashboard renders "no fix" for
// empty FixedIn.
//
// Paths is the per-file path list for the matched
// artifact (grype's artifact.locations[].path). Populated
// by parseGrypeOutput in pkg/imaged/grype.go — the slim
// per-PR-1 grypeMatch is now extended with the artifact
// block, and the JSON tag is mirrored byte-identically
// on pkg/imaged.Vulnerability. Empty when the CVE matches
// against a package without a specific file path
// (deb/apt-style CVE matches); the jsonb omitempty drops
// the field so a no-path row stays compact.
type Vulnerability struct {
	ID       string   `json:"id"`                 // e.g. "CVE-2024-1234"
	Severity string   `json:"severity"`           // CRITICAL|HIGH|MEDIUM|LOW|UNKNOWN
	Package  string   `json:"package"`            // e.g. "openssl"
	Version  string   `json:"version"`            // e.g. "1.1.1k-7"
	FixedIn  string   `json:"fixed_in,omitempty"` // "" when no fix is available
	Paths    []string `json:"paths,omitempty"`    // file paths within the scanned dir; nil/empty when no path was reported
}
