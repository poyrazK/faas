/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { SecretFinding } from './SecretFinding.js';
/**
 * Customer-facing wire shape of one deployment's secret-scan
 * audit row. Mirrors the `ScanResult` shape but for the
 * secret-scan pipeline. The `status` enum is the closed set
 * the imaged-side writer (PR-A) stamps: `complete` for a
 * clean walk, `complete_with_redactions` for a hit (mirrors
 * the v2 widening on `deployments_scan_status_chk` from
 * migration 00264). `image_digest` is the OCI digest the
 * scan ran against (PR-A) — mirrors
 * `ScanResult.image_digest` so a side-by-side compare
 * renders both scans against the same bytes. `findings[]`
 * may be empty (clean walk); the field is always present
 * for round-trip JSON stability.
 *
 */
export type SecretScanResult = {
  status: string;
  /**
   * RFC 3339 UTC. Empty when the deployment hasn't been
   * secret-scanned yet.
   *
   */
  scanned_at?: string;
  /**
   * OCI digest the scan ran against (PR-A). Mirrors
   * `ScanResult.image_digest` for cross-pipeline
   * comparison. Empty for pre-PR-A rows.
   *
   */
  image_digest?: string;
  findings: Array<SecretFinding>;
  /**
   * In-band explanation when the audit row is unreadable
   * (jsonb decode failed). Mirrors `ScanResult.error`.
   * Empty on the success path — `status` carries the
   * closed-set signal there.
   *
   */
  error?: string;
};

