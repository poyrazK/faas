/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsAlertCollectionHealth } from './ObsAlertCollectionHealth.js';
import type { ObsAlertRetentionHealth } from './ObsAlertRetentionHealth.js';
import type { ObsAlertWorkloadCounts } from './ObsAlertWorkloadCounts.js';
import type { ObsAlertWorkloadGroup } from './ObsAlertWorkloadGroup.js';
import type { ObsAlertWorkloadOldest } from './ObsAlertWorkloadOldest.js';
/**
 * Atomic retained-alert workload snapshot with explicit collection and cleanup coverage.
 */
export type ObsAlertWorkloadSummary = {
  as_of: string;
  scope: 'retained_platform_alert_episodes';
  age_basis: 'episode_started_at';
  collection: ObsAlertCollectionHealth;
  retention: ObsAlertRetentionHealth;
  /**
   * Null until a successful collection has established a retained observation scope.
   */
  counts: (ObsAlertWorkloadCounts | null);
  groups: Array<ObsAlertWorkloadGroup>;
  /**
   * Oldest unsnoozed unresolved unacknowledged episode; null when absent or scope is unknown.
   */
  oldest_unacknowledged: (ObsAlertWorkloadOldest | null);
  /**
   * Oldest unsnoozed unresolved unowned episode; null when absent or scope is unknown.
   */
  oldest_unowned: (ObsAlertWorkloadOldest | null);
};

