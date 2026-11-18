/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeCommandCronTimingSignal } from './ObsRuntimeCommandCronTimingSignal.js';
import type { ObsRuntimeProcessItem } from './ObsRuntimeProcessItem.js';
/**
 * Sanitized scheduler process proof and 13 ordered timing signals. Exact target deltas count timed observations, cumulative delay buckets and unknown timestamps separately. Details yield first under the shared response cap; full-set signal coverage and explicit omissions survive. No cron, command, occurrence, task or app identity or raw target address is exposed.
 */
export type ObsRuntimeCommandCronTimingItem = {
  job: 'schedd' | 'schedd-compute';
  node?: string;
  target_id?: string;
  unexpected?: boolean;
  scrape_observed_at?: string;
  process: ObsRuntimeProcessItem;
  signals: Array<ObsRuntimeCommandCronTimingSignal>;
};

