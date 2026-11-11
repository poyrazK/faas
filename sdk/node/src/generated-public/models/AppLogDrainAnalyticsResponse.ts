/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppLogDrainAnalyticsBucket } from './AppLogDrainAnalyticsBucket.js';
import type { AppLogDrainAnalyticsSummary } from './AppLogDrainAnalyticsSummary.js';
/**
 * Bounded hourly, customer-safe delivery analytics for one runtime log destination.
 */
export type AppLogDrainAnalyticsResponse = {
  /**
   * Original requested lower bound before expanding to the recorded hourly observations.
   */
  requested_from?: string;
  /**
   * True when hourly observations changed the effective bounds; from/to match the counter observation times.
   */
  window_aligned?: boolean;
  /**
   * Whether every reported counter delta has a valid baseline without missing hourly samples.
   */
  complete?: boolean;
  /**
   * Missing baseline or hourly sample evidence; unknown deltas are omitted from totals.
   */
  coverage_gaps?: Array<string>;
  log_drain_id: string;
  window: '1h' | '24h' | '7d' | '30d';
  bucket_interval: '1h';
  from: string;
  to: string;
  buckets: Array<AppLogDrainAnalyticsBucket>;
  summary: AppLogDrainAnalyticsSummary;
};

