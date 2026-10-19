/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsAlertCollectionCoverage } from './ObsAlertCollectionCoverage.js';
import type { ObsAlertCollectionHealth } from './ObsAlertCollectionHealth.js';
import type { ObsAlertResponseAggregate } from './ObsAlertResponseAggregate.js';
import type { ObsAlertResponseGroup } from './ObsAlertResponseGroup.js';
import type { ObsAlertRetentionHealth } from './ObsAlertRetentionHealth.js';
/**
 * Atomic retained activation-cohort timing with explicit evidence and sample limitations.
 */
export type ObsAlertResponseTimes = {
  /**
   * Collection cadence evidence captured with this timing cohort; older servers omit it and retained timing samples do not imply complete monitoring.
   */
  coverage?: ObsAlertCollectionCoverage;
  as_of: string;
  since: string;
  scope: 'retained_activation_cohort';
  duration_basis: 'episode_activation';
  percentile_method: 'nearest_rank_whole_seconds';
  limitations: Array<string>;
  collection: ObsAlertCollectionHealth;
  retention: ObsAlertRetentionHealth;
  /**
   * Null before any successful collection; a known quiet retained cohort has zero counts and null percentiles.
   */
  totals: (ObsAlertResponseAggregate | null);
  groups: Array<ObsAlertResponseGroup>;
};

