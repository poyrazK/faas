/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FieldError } from './FieldError.js';
import type { LogExcerpt } from './LogExcerpt.js';
import type { SecretFinding } from './SecretFinding.js';
/**
 * RFC 7807 problem+json envelope. The `code` field is the stable
 * machine-readable identifier; clients branch on it. `limit` and
 * `observed` are populated on quota errors. `docs_url` points the
 * user at the next action. `billing_portal_url` is populated on
 * `code: payment_required` when the customer already has a
 * provider subscription and must update it in the provider
 * portal. `checkout_url` is populated when a new hosted checkout
 * is required. `paddle_checkout_url` is retained as a legacy
 * alias for Paddle clients, and `tx_id` carries the provider
 * checkout handle when one exists.
 *
 * `errors` carries per-field detail (Cloudflare / Stripe shape)
 * for 422 sites that emit a list of field-level failures — used
 * today by the kind=validate edge rule so a JSON Schema
 * rejection renders as a form-field list the dashboard can
 * iterate without parsing prose. Optional + omitempty so every
 * other problem+json site keeps its existing flat shape unchanged.
 *
 */
export type Problem = {
  type?: string;
  title: string;
  status: number;
  /**
   * Stable machine-readable error code. See StatusForCode in pkg/api/errors.go.
   */
  code: string;
  detail?: string;
  limit?: number | null;
  observed?: number | null;
  docs_url?: string;
  /**
   * Provider-neutral hosted checkout URL on a `payment_required`
   * 402 when a paid plan upgrade requires a new subscription.
   *
   */
  checkout_url?: string;
  billing_portal_url?: string;
  /**
   * Legacy Paddle-hosted checkout URL on a `payment_required`
   * 402. Prefer the provider-neutral `checkout_url` field.
   *
   */
  paddle_checkout_url?: string;
  /**
   * Paddle transaction handle (`txn_…`) on a `payment_required`
   * 402. Empty on the Stripe path. The dashboard renders this as
   * a confirmation id after the customer completes checkout.
   *
   */
  tx_id?: string;
  /**
   * Per-field validation detail. Populated by 422 sites that
   * emit a list of field-level failures. Each entry is a
   * `FieldError` (Cloudflare / Stripe shape: field + expected
   * + got) so an SDK can drive form-field UI without parsing
   * prose.
   *
   */
  errors?: Array<FieldError>;
  /**
   * Per-line secret-scan detail. Populated by 422 sites with
   * `code: secret_scan_strict` (cmd/apid/secretscan.go
   * server-side scan rejection; cmd/gregale printErr
   * --secret-scan=strict client-side rejection). The shape
   * is shared with the on-disk `SecretScanResult` so a
   * programmatic consumer can render the same UI for both
   * rejection paths. Optional + omitempty.
   *
   */
  secret_findings?: Array<SecretFinding>;
  /**
   * Customer-facing remediation nudge attached to a
   * `code: secret_scan_strict` 422 envelope (e.g. "move
   * detected secrets to `gregale secrets set`"). Mirrors
   * the `FieldError` shape's prose pattern so the dashboard
   * / SDK can render the hint as a one-line footer without
   * parsing prose. Optional + omitempty.
   *
   */
  secret_hint?: string;
  /**
   * Single short next-action line lifted from the
   * `pkg/whycopy` catalog (error-explanations cluster,
   * spec §6.4 amendment 1). Populated by the 9 cluster-
   * owned RFC 7807 codes (app_not_listening,
   * app_loopback_bound, app_arch_mismatch,
   * env_var_missing, app_healthz_unauthorized,
   * app_runtime_oom, dep_install_failed,
   * app_startup_timeout, stateless_only_violation). The
   * CLI renders this as the first line of the 5-line
   * error shape (`hint: <hint>`). Optional + omitempty
   * so every other problem+json site keeps its existing
   * 3-line shape unchanged.
   *
   */
  hint?: string;
  /**
   * Human-readable cause with the observed value templated
   * in (error-explanations cluster, spec §6.4 amendment 1).
   * Distinct from `detail`: `detail` is the platform's
   * machine-stable message; `why` is the customer-facing
   * explanation. Multi-line (≤512 bytes per `pkg/whycopy`
   * catalog row). Optional + omitempty.
   *
   */
  why?: string;
  /**
   * Prescriptive remediation (1-3 lines, error-explanations
   * cluster, spec §6.4 amendment 1). Distinct from `hint`:
   * `hint` is a single line, `fix` is the bulleted
   * remediation list. The CLI renders this as
   * `→ fix: <fix>` with literal newlines preserved so the
   * multi-line shape survives. Optional + omitempty.
   *
   */
  fix?: string;
  /**
   * Per-line log excerpts that explain the failure (error-
   * explanations cluster, spec §6.4 amendment 1). The
   * detection site attaches the last N log lines that
   * caused the failure (capped at 20 entries × 512 bytes
   * each per CLI tripwire). The CLI renders the first 5
   * inline as a fenced block. Optional + omitempty.
   *
   */
  relevant_logs?: Array<LogExcerpt>;
};

