/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsNotificationReceiver } from './ObsNotificationReceiver.js';
import type { ObsNotificationSignal } from './ObsNotificationSignal.js';
/**
 * Independent receiver assessment, bounded to 32 receiver/integration groups.
 * Deployment intent is independent of actual collector output. Healthy
 * requires fresh collection and inventory, complete counter increases and
 * an observed successful transport clock for every expected group. Partial
 * preserves unavailable evidence, insufficient counter samples and unknown
 * success history. Degraded identifies failed requests, missing expected
 * transport telemetry or a freshly observed group outside deployment intent.
 * This section does not change the top-level aggregate assessment.
 *
 */
export type ObsNotificationReceiverHealth = {
  state: 'healthy' | 'partial' | 'degraded';
  reason?: string;
  /**
   * Fresh read quality for the collector flag and its own run clock. A known zero means collection failed; unavailable or stale evidence has a null value.
   */
  collector: ObsNotificationSignal;
  /**
   * Expected group count from fresh deployment intent. Missing or inconsistent intent remains null, never zero configured receivers.
   */
  inventory: ObsNotificationSignal;
  /**
   * Reviewed receiver/integration groups ordered by receiver then integration. Addresses, arbitrary names, scrape identities and provider errors are excluded.
   */
  groups: Array<ObsNotificationReceiver>;
  limitations: Array<string>;
};

