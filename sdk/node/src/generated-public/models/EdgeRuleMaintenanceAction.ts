/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Per-route maintenance toggle (ADR-091 amendment, §4.1.2.13).
 * The fine-grained primitive for "this endpoint is in
 * maintenance mode" — the hot-path applier
 * (pkg/gateway.(*Handler).applyEdgeRuleMaintenance) short-
 * circuits a matched (host, path, http_method) request with
 * 503 + Retry-After BEFORE auth, BEFORE wake. The coarse
 * sibling (`apps.maintenance_mode`, PATCH /v1/apps/{slug})
 * covers the "whole app in maintenance" case; use this kind
 * when only specific routes need to be pinned.
 *
 * Field-by-field:
 * * `retry_after_seconds` — optional per-rule override for
 * the `Retry-After` header. 0 (default) = use the
 * platform default
 * `api.EdgeRuleMaintenanceRetryAfterSeconds` (60 s).
 * Must be in `[0, 86400]` (24 h, enforced by
 * `api.MaxEdgeRuleMaintenanceRetryAfterSeconds`); a
 * customer cannot ship a rule that asks a client to
 * back off for a week.
 * * `message` — optional operator-friendly string that
 * goes into `Problem.detail`. ≤ 512 B (same payload-
 * size budget as `EdgeRuleValidateAction.schema`).
 * Surface this on the customer's status page or in a
 * dashboard alert so operators see why the endpoint is
 * dark without scraping the rule row.
 *
 * Free-and-above (no plan gate). Mirror the validate /
 * limit posture: rejection never reaches the wake gate, the
 * auth chain, or the rate limiter.
 *
 */
export type EdgeRuleMaintenanceAction = {
  /**
   * Optional per-rule Retry-After override in seconds.
   * 0 (default) = use the platform default
   * `api.EdgeRuleMaintenanceRetryAfterSeconds` (60 s).
   * Must be in `[0, 86400]` (24 h); values above 86400 are
   * rejected at create-time with 422.
   *
   */
  retry_after_seconds?: number;
  /**
   * Optional operator-friendly detail string that goes into
   * `Problem.detail`. ≤ 512 B. Surface this on the
   * customer's status page so monitoring / curl users see
   * why the endpoint is dark.
   *
   */
  message?: string;
};

