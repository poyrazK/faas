/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsAlertCollectionCoverage } from './ObsAlertCollectionCoverage.js';
import type { ObsAlertCollectionFailureDetails } from './ObsAlertCollectionFailureDetails.js';
import type { ObsAlertCollectionHealth } from './ObsAlertCollectionHealth.js';
import type { ObsAlertCollectionHistoryInterval } from './ObsAlertCollectionHistoryInterval.js';
/**
 * Complete bounded collection cadence totals with a newest-first interval preview and current collection metadata.
 */
export type ObsAlertCollectionHistory = {
  /**
   * Opt-in failed-attempt evidence from the same read snapshot; omitted unless details=true and absent on older servers.
   */
  failure_details?: ObsAlertCollectionFailureDetails;
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

