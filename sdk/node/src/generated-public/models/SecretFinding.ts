/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Per-line entry of `Problem.secret_findings` AND
 * `SecretScanResult.findings` (issue #862 + secret-scan v2,
 * PR #873; PR-A 101 extends the surfaced shape via the new
 * `layer` field). The shape mirrors `pkg/secretscan.Finding`
 * but is decoupled so the wire schema can evolve
 * independently of the scanner's internal fields. `snippet`
 * is the pre-truncated safe representation (first 6 chars +
 * "…" + last 4) — never the raw value, matching the snippet
 * policy documented in `pkg/secretscan/scan.go`. Closed-set
 * `provider` keys: stripe_live, stripe_test, github_pat,
 * aws_access, openai, anthropic, private_key_block. The
 * optional `layer` field (PR-A) names the per-walk source
 * label — "app" for the main image, "sidecar-<slug>" for
 * each sidecar, or absent for the apid source-tree
 * rejection path (legacy).
 *
 */
export type SecretFinding = {
  file: string;
  line: number;
  key?: string;
  provider: string;
  severity: 'high' | 'medium';
  snippet: string;
  /**
   * Per-walk source label (PR-A). "app" for findings in
   * the main image; "sidecar-<slug>" for findings in a
   * sidecar (e.g. "sidecar-redis"); absent or "" for
   * findings from the legacy apid source-tree rejection
   * path (cmd/apid/secretscan.go).
   *
   */
  layer?: string;
};

