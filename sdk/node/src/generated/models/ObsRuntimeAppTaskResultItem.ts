/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeAppTaskResultSignal } from './ObsRuntimeAppTaskResultSignal.js';
import type { ObsRuntimeProcessItem } from './ObsRuntimeProcessItem.js';
/**
 * Sanitized scheduler process proof and 18 ordered completion/recovery signals. Comparison time matches the core process section. Preview details yield before existing evidence under the shared response limit; aggregate counts and omissions survive. No command, task, app identity, output, lease token, raw target address or provider error is exposed.
 */
export type ObsRuntimeAppTaskResultItem = {
  job: 'schedd' | 'schedd-compute';
  node?: string;
  target_id?: string;
  unexpected?: boolean;
  scrape_observed_at?: string;
  process: ObsRuntimeProcessItem;
  signals: Array<ObsRuntimeAppTaskResultSignal>;
};

