/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Wire shape for `GET /v1/apps/{slug}/usage?since=&until=`
 * (commit 4 of the per-app observability PR series).
 * Plan-gated Hobby+; Free falls through with
 * `plan_app_usage_summary_not_allowed`.
 *
 * `gb_hours` is the rounded float of `mb_seconds / 1024 /
 * 3600` (mirror of `meter.MonthlyUsageGB`'s 6-decimal
 * rounding). `overage_gb_hours = max(0, gb_hours -
 * plan_included_gb_hours)` — 0 when the customer is under
 * their included band, the billable overage above the band
 * otherwise. The Stripe pusher bills overage at €0.01/GB-h
 * (CLAUDE.md integer-cents-only rule).
 *
 */
export type AppUsageSummaryResponse = {
  slug: string;
  /**
   * Half-open [period_start, period_end) window's inclusive lower bound. UTC.
   */
  period_start: string;
  /**
   * Half-open window's exclusive upper bound. UTC midnight snap (handler defaults).
   */
  period_end: string;
  /**
   * Sum of usage_minutes.mb_seconds for this app in the window (ADR-048 billable unit).
   */
  mb_seconds: number;
  /**
   * mb_seconds / 1024 / 3600, rounded to 6 decimal places (mirror of meter.MonthlyUsageGB).
   */
  gb_hours: number;
  /**
   * Cumulative HTTP request count (informational; not billed).
   */
  requests: number;
  /**
   * Cumulative HTTP response body bytes (ADR-046; diagnostic subset of net_tx_bytes; never add both).
   */
  tx_bytes: number;
  /**
   * Canonical cumulative host-interface egress bytes for the window (ADR-046; includes framing).
   */
  net_tx_bytes: number;
  /**
   * Cumulative builder-microVM CPU-seconds (informational; surfaced as a sidebar line on the dashboard).
   */
  builder_seconds: number;
  /**
   * WAKE_RESTORE→WAKE_COLD_BOOT transitions in the window.
   */
  cold_boot_count: number;
  /**
   * Echoed from acct.Plan.PlanIncludedGBHours() — plan's per-month included band so the dashboard renders the included-band badge without a second round-trip.
   */
  plan_included_gb_hours: number;
  /**
   * max(0, gb_hours - plan_included_gb_hours). 0 when the customer is under their included band; the billable overage above the band otherwise.
   */
  overage_gb_hours: number;
  /**
   * Which rollup reader produced this summary. usage_minutes today (30d retention); usage_daily lands with the trail-period follow-up.
   */
  source: 'usage_minutes' | 'usage_daily' | 'mixed';
  /**
   * RFC3339Nano UTC stamping the envelope's authoritative 'as of' instant.
   */
  as_of: string;
};

