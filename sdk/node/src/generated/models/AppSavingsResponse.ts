/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Wire shape for `GET /v1/apps/{slug}/savings?since=&until=`.
 * A scale-to-zero savings estimate: billed RAM-time compared
 * with an always-on counterfactual of `baseline_instances`
 * instances of `billable_ram_mb` running from `baseline_start`
 * to `period_end`. `saved_* = max(0, always_on_* - actual_*)`.
 * Money fields are integer millicents at
 * `price_millicents_per_gb_hour` (the plan overage rate). This
 * is an estimate, not an invoice line; `methodology` carries
 * the sentence clients print under the figure.
 *
 */
export type AppSavingsResponse = {
  slug: string;
  /**
   * Inclusive lower bound of the resolved window, UTC midnight, never earlier than period_end - 30d.
   */
  period_start: string;
  /**
   * Exclusive upper bound of the resolved window, UTC midnight.
   */
  period_end: string;
  /**
   * Start of the always-on counterfactual: the later of period_start and the app's first billed hour. Equals period_end when the app billed nothing.
   */
  baseline_start: string;
  /**
   * max(min_instances, 1) — the always-on instance floor.
   */
  baseline_instances: number;
  /**
   * Per-instance plan RAM + per-VM overhead used for the counterfactual. Companion sidecars are excluded, so the estimate is conservative.
   */
  billable_ram_mb: number;
  always_on_mb_seconds: number;
  /**
   * Billed mb_seconds in the window (same source as /usage).
   */
  actual_mb_seconds: number;
  saved_mb_seconds: number;
  always_on_gb_hours: number;
  actual_gb_hours: number;
  /**
   * GB-hour fields are rounded to 6 decimal places.
   */
  saved_gb_hours: number;
  price_millicents_per_gb_hour: number;
  always_on_millicents: number;
  actual_millicents: number;
  saved_millicents: number;
  /**
   * saved_mb_seconds / always_on_mb_seconds.
   */
  parked_ratio: number;
  methodology: string;
  source: 'usage_minutes' | 'usage_daily' | 'mixed';
  as_of: string;
};

