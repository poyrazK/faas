/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeCommandCronClass } from './ObsRuntimeCommandCronClass.js';
import type { ObsRuntimeCommandCronObservation } from './ObsRuntimeCommandCronObservation.js';
import type { ObsRuntimeCommandCronSweep } from './ObsRuntimeCommandCronSweep.js';
import type { ObsRuntimeHandoffCollection } from './ObsRuntimeHandoffCollection.js';
/**
 * One sanitized scheduler ownership scope, identified by selected job and target. Collection, observer expectation, live scan activity and ten durable schedule classes remain distinct. No schedule, app, account, command or payload identity is exposed.
 */
export type ObsRuntimeCommandCronItem = {
  job: 'schedd' | 'schedd-compute';
  node?: string;
  target_id?: string;
  unexpected?: boolean;
  scrape_observed_at?: string;
  state: 'available' | 'missing' | 'stale' | 'conflicting' | 'unavailable';
  collection: ObsRuntimeHandoffCollection;
  observation: ObsRuntimeCommandCronObservation;
  sweep: ObsRuntimeCommandCronSweep;
  classes: Array<ObsRuntimeCommandCronClass>;
};

