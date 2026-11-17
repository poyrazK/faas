/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeAppTaskClass } from './ObsRuntimeAppTaskClass.js';
import type { ObsRuntimeAppTaskDispatch } from './ObsRuntimeAppTaskDispatch.js';
import type { ObsRuntimeAppTaskObservation } from './ObsRuntimeAppTaskObservation.js';
import type { ObsRuntimeHandoffCollection } from './ObsRuntimeHandoffCollection.js';
/**
 * One sanitized scheduler replica. Collection status, observer expectation, local dispatcher and 27 ordered kind/class observations are validated independently. No task, app, command or output identifiers are exposed.
 */
export type ObsRuntimeAppTaskItem = {
  job: 'schedd' | 'schedd-compute';
  node?: string;
  target_id?: string;
  unexpected?: boolean;
  scrape_observed_at?: string;
  state: 'available' | 'missing' | 'stale' | 'conflicting' | 'unavailable';
  collection: ObsRuntimeHandoffCollection;
  observation: ObsRuntimeAppTaskObservation;
  dispatch: ObsRuntimeAppTaskDispatch;
  classes: Array<ObsRuntimeAppTaskClass>;
};

