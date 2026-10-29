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
  log_drain_id: string;
  window: '1h' | '24h' | '7d' | '30d';
  bucket_interval: '1h';
  from: string;
  to: string;
  buckets: Array<AppLogDrainAnalyticsBucket>;
  summary: AppLogDrainAnalyticsSummary;
};

