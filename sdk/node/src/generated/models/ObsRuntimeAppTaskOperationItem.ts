/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeAppTaskOperationSignal } from './ObsRuntimeAppTaskOperationSignal.js';
import type { ObsRuntimeProcessItem } from './ObsRuntimeProcessItem.js';
/**
 * One sanitized scheduler process observation. Retains current scrape evidence, sampled process continuity proof and 35 ordered operation/outcome signals. No command, task, app, raw target address or provider error is exposed. Root comparison time must match the identity section.
 */
export type ObsRuntimeAppTaskOperationItem = {
  job: 'schedd' | 'schedd-compute';
  node?: string;
  target_id?: string;
  unexpected?: boolean;
  scrape_observed_at?: string;
  process: ObsRuntimeProcessItem;
  signals: Array<ObsRuntimeAppTaskOperationSignal>;
};

