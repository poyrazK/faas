/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeDelayedTaskDispatchSignal } from './ObsRuntimeDelayedTaskDispatchSignal.js';
import type { ObsRuntimeProcessItem } from './ObsRuntimeProcessItem.js';
/**
 * Sanitized scheduler process proof and four ordered dispatch counters. Preview details yield first under the shared response cap, preserving full-set summaries and explicit omissions. No task, invocation, app, account, response body, raw target address or provider error is exposed.
 */
export type ObsRuntimeDelayedTaskDispatchItem = {
  job: 'schedd' | 'schedd-compute';
  node?: string;
  target_id?: string;
  unexpected?: boolean;
  scrape_observed_at?: string;
  process: ObsRuntimeProcessItem;
  signals: Array<ObsRuntimeDelayedTaskDispatchSignal>;
};

