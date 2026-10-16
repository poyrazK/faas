/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsAlertCollectionCoverage } from './ObsAlertCollectionCoverage.js';
import type { ObsAlertCollectionHealth } from './ObsAlertCollectionHealth.js';
import type { ObsAlertCollectionHistoryInterval } from './ObsAlertCollectionHistoryInterval.js';
/**
 * Complete bounded collection cadence totals with a newest-first interval preview and current collection metadata.
 */
export type ObsAlertCollectionHistory = {
  coverage: ObsAlertCollectionCoverage;
  collection: ObsAlertCollectionHealth;
  items: Array<ObsAlertCollectionHistoryInterval>;
  total_intervals: number;
  limit: number;
  /**
   * Whether older coalesced intervals are omitted from this preview while coverage totals remain complete.
   */
  truncated: boolean;
};

