/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeHTTPCronDeliverySignal } from './ObsRuntimeHTTPCronDeliverySignal.js';
import type { ObsRuntimeProcessItem } from './ObsRuntimeProcessItem.js';
/**
 * Sanitized scheduler process proof and five ordered delivery outcome counters. Preview details yield first under the shared response cap, preserving full-set summaries and explicit omissions. No app, cron, invocation, path, response body, raw target address or provider error is exposed.
 */
export type ObsRuntimeHTTPCronDeliveryItem = {
  job: 'schedd' | 'schedd-compute';
  node?: string;
  target_id?: string;
  unexpected?: boolean;
  scrape_observed_at?: string;
  process: ObsRuntimeProcessItem;
  signals: Array<ObsRuntimeHTTPCronDeliverySignal>;
};

