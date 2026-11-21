/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeHandoffEvidenceReason } from './ObsRuntimeHandoffEvidenceReason.js';
import type { ObsRuntimePreparationObservationValues } from './ObsRuntimePreparationObservationValues.js';
/**
 * Whether the preparation observer is expected, independently of collection success and pending work. A false enabled bit can coexist with pending checkpoints.
 */
export type ObsRuntimePreparationObservation = {
  state: 'available' | 'missing' | 'stale' | 'conflicting' | 'unavailable';
  reason?: ObsRuntimeHandoffEvidenceReason;
  observed_at?: string;
  values?: ObsRuntimePreparationObservationValues;
};

