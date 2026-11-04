/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppErrorSummaryItem } from './AppErrorSummaryItem.js';
/**
 * Grouped error summary returned by `GET /v1/apps/{slug}/errors/summary`. One row per fingerprint over the
 * requested `[since, until]` window. Distinct from
 * `AppSLOResponse` which is the closed-set SLO
 * summary — the errors summary uses a continuous window
 * with explicit RFC3339Nano stamps.
 *
 * Empty result returns 200 with `items: []` and the
 * window echo set. `window_clamped` is true when the
 * requested span was clamped to `AppErrorsWindowMaxHours`
 * (168h). `next_cursor` is empty when the page is the
 * last one.
 *
 */
export type AppErrorsSummaryResponse = {
  /**
   * RFC3339Nano UTC stamp at which the summary was assembled.
   */
  generated_at: string;
  app_id: string;
  app_slug: string;
  /**
   * Echoed (clamped) window start, RFC3339Nano UTC.
   */
  window_start: string;
  /**
   * Echoed (clamped) window end, RFC3339Nano UTC.
   */
  window_end: string;
  /**
   * True when the requested span was clamped to AppErrorsWindowMaxHours (168h).
   */
  window_clamped: boolean;
  items: Array<AppErrorSummaryItem>;
  /**
   * Opaque base64 cursor for the next page. Empty when the current page is the last.
   */
  next_cursor?: string | null;
  /**
   * Echoed limit applied (post-clamp to AppErrorsSummaryMaxLimit=100).
   */
  limit: number;
};

