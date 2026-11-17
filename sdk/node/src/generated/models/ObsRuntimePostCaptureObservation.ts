/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeHandoffEvidenceReason } from './ObsRuntimeHandoffEvidenceReason.js';
import type { ObsRuntimePostCaptureObservationValues } from './ObsRuntimePostCaptureObservationValues.js';
/**
 * Whether post-capture observation is expected, independently of collection success and class evidence. A disabled observer can retain a fresh nonempty view.
 */
export type ObsRuntimePostCaptureObservation = {
  state: 'available' | 'missing' | 'stale' | 'conflicting' | 'unavailable';
  reason?: ObsRuntimeHandoffEvidenceReason;
  observed_at?: string;
  values?: ObsRuntimePostCaptureObservationValues;
};

