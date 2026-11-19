/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeAppTaskObservationValues } from './ObsRuntimeAppTaskObservationValues.js';
import type { ObsRuntimeHandoffEvidenceReason } from './ObsRuntimeHandoffEvidenceReason.js';
/**
 * Whether durable task observation is expected, independently of collection success, dispatch configuration and runner activity. Disabled observation does not imply drained tasks.
 */
export type ObsRuntimeAppTaskObservation = {
  state: 'available' | 'missing' | 'stale' | 'conflicting' | 'unavailable';
  reason?: ObsRuntimeHandoffEvidenceReason;
  observed_at?: string;
  values?: ObsRuntimeAppTaskObservationValues;
};

