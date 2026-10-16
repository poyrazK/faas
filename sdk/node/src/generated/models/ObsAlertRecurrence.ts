/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsAlertCollectionCoverage } from './ObsAlertCollectionCoverage.js';
import type { ObsAlertCollectionHealth } from './ObsAlertCollectionHealth.js';
import type { ObsAlertRecurrenceSeries } from './ObsAlertRecurrenceSeries.js';
import type { ObsAlertRecurrenceTotals } from './ObsAlertRecurrenceTotals.js';
import type { ObsAlertRetentionHealth } from './ObsAlertRetentionHealth.js';
/**
 * Retained activation-cohort recurrence evidence with complete totals and a ranked preview.
 */
export type ObsAlertRecurrence = {
  /**
   * Prospective collection cadence evidence from the same snapshot; absent on older servers and never proof of complete alert history.
   */
  coverage?: ObsAlertCollectionCoverage;
  as_of: string;
  since: string;
  scope: 'retained_activation_cohort';
  percentile_method: 'nearest_rank_whole_seconds';
  limitations: Array<string>;
  collection: ObsAlertCollectionHealth;
  retention: ObsAlertRetentionHealth;
  /**
   * Null before the first successful collection; a known quiet retained cohort has zero counts and null statistics.
   */
  totals: (ObsAlertRecurrenceTotals | null);
  items: Array<ObsAlertRecurrenceSeries>;
  /**
   * Whether the ranked preview omits series included in complete totals.
   */
  truncated: boolean;
};

