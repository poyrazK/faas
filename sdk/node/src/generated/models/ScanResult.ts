/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { SeverityCounts } from './SeverityCounts.js';
import type { Vulnerability } from './Vulnerability.js';
/**
 * Per-deploy grype CVE scan result (issue #464 / ADR-055). Surfaced on
 * GET /v1/deployments/{id} (additive DeploymentResponse.Scan field) and
 * on GET /v1/deployments/{id}/scan (the dedicated drill-down route).
 * The dashboard renders the severity counts and the top 10 CVEs;
 * `gregale deployment <id> --show-scan` prints the full payload.
 * Surface for off/warn apps. With security_policy=enforce, the
 * deployment is promoted only after a complete, digest-matched scan
 * with no HIGH, CRITICAL, or UNKNOWN findings.
 *
 */
export type ScanResult = {
  /**
   * Closed enum mirroring the deployments.scan_status column.
   * `pending` = grype run started, not finished yet;
   * `complete` = grype run finished, scan carries the findings;
   * `failed` = grype run errored after the 1-retry backoff, scan carries the last error in `error`;
   * `skipped` = pre-feature row (the migration backfilled this on every row that predates 00135).
   *
   */
  status: 'pending' | 'complete' | 'failed' | 'skipped';
  /**
   * Wall clock the grype run completed (RFC 3339 UTC). Empty when status != "complete". Distinct from deployments.created_at — the deploy ships before the scan lands (AC
   */
  scanned_at?: string | null;
  /**
   * Grype binary version that produced the scan (e.g. "grype 0.78.0"). Captured once at imaged startup via `grype version` and stamped on every ScanResult payload.
   */
  scanner_version?: string | null;
  /**
   * OCI image reference recorded at the time of the scan. In security_policy=enforce, this must exactly match deployments.image_digest before promotion. Empty on the pre-feature backfill (status = "skipped" with no image to stamp).
   */
  image_digest?: string | null;
  severity_counts: SeverityCounts;
  /**
   * Full CVE list, ordered by Grype's natural output (most-severe-first). The dashboard's "top 10" view sorts+truncates client-side. The /scan route returns the full list.
   */
  vulnerabilities: Array<Vulnerability>;
  /**
   * Grype runner's last error message on a failed scan (status = "failed"). Empty on every other status. The PR-3 sink captures the message after the 1-retry backoff is exhausted.
   */
  error?: string | null;
};

