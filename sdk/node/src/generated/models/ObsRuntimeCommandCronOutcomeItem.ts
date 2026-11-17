/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeCommandCronOutcomeSignal } from './ObsRuntimeCommandCronOutcomeSignal.js';
import type { ObsRuntimeProcessItem } from './ObsRuntimeProcessItem.js';
/**
 * Sanitized scheduler process proof and 29 ordered source/outcome signals. The comparison clock matches the core process section. Preview details yield first under the shared response limit, preserving aggregate coverage and explicit omissions. No cron, command, occurrence, task or app identity, raw target address, output, token or provider error is exposed.
 */
export type ObsRuntimeCommandCronOutcomeItem = {
  job: 'schedd' | 'schedd-compute';
  node?: string;
  target_id?: string;
  unexpected?: boolean;
  scrape_observed_at?: string;
  process: ObsRuntimeProcessItem;
  signals: Array<ObsRuntimeCommandCronOutcomeSignal>;
};

