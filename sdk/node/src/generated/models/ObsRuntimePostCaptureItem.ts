/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeHandoffCollection } from './ObsRuntimeHandoffCollection.js';
import type { ObsRuntimePostCaptureClass } from './ObsRuntimePostCaptureClass.js';
import type { ObsRuntimePostCaptureObservation } from './ObsRuntimePostCaptureObservation.js';
/**
 * One sanitized image worker replica view of the shared fleet, retaining job and target identity. Collection status, observer expectation and eight class observations are independently validated. No customer, app or deployment identity is exposed.
 */
export type ObsRuntimePostCaptureItem = {
  job: 'imaged';
  node?: string;
  target_id?: string;
  unexpected?: boolean;
  scrape_observed_at?: string;
  state: 'available' | 'missing' | 'stale' | 'conflicting' | 'unavailable';
  collection: ObsRuntimeHandoffCollection;
  observation: ObsRuntimePostCaptureObservation;
  classes: Array<ObsRuntimePostCaptureClass>;
};

