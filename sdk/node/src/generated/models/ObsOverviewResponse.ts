/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsOverviewFailureKind } from './ObsOverviewFailureKind.js';
import type { ObsOverviewNodeHealth } from './ObsOverviewNodeHealth.js';
import type { ObsOverviewRateLimited } from './ObsOverviewRateLimited.js';
import type { ObsOverviewTotals } from './ObsOverviewTotals.js';
/**
 * Fleet overview - totals, node health, and bounded failure buckets.
 */
export type ObsOverviewResponse = {
  generated_at: string;
  totals: ObsOverviewTotals;
  top_rate_limited_accounts_24h: Array<ObsOverviewRateLimited>;
  node_health: Array<ObsOverviewNodeHealth>;
  recent_failures_1h: Array<ObsOverviewFailureKind>;
};

