/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeHandoffCollection } from './ObsRuntimeHandoffCollection.js';
import type { ObsRuntimeSnapshotPrimeClass } from './ObsRuntimeSnapshotPrimeClass.js';
import type { ObsRuntimeSnapshotPrimeObservation } from './ObsRuntimeSnapshotPrimeObservation.js';
/**
 * One sanitized scheduler ownership scope, identified by its selected job and target. Independent collection status, observer expectation and six ordered class observations contain no app or deployment identities.
 */
export type ObsRuntimeSnapshotPrimeItem = {
  job: 'schedd' | 'schedd-compute';
  node?: string;
  target_id?: string;
  unexpected?: boolean;
  scrape_observed_at?: string;
  state: 'available' | 'missing' | 'stale' | 'conflicting' | 'unavailable';
  collection: ObsRuntimeHandoffCollection;
  observation: ObsRuntimeSnapshotPrimeObservation;
  classes: Array<ObsRuntimeSnapshotPrimeClass>;
};

