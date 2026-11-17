/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeHandoffEvidenceReason } from './ObsRuntimeHandoffEvidenceReason.js';
import type { ObsRuntimeSnapshotPrimeObservationValues } from './ObsRuntimeSnapshotPrimeObservationValues.js';
/**
 * Whether snapshot-prime observation is expected, independently of collection success and attempt evidence. Disabled observation can coexist with nonempty classes.
 */
export type ObsRuntimeSnapshotPrimeObservation = {
  state: 'available' | 'missing' | 'stale' | 'conflicting' | 'unavailable';
  reason?: ObsRuntimeHandoffEvidenceReason;
  observed_at?: string;
  values?: ObsRuntimeSnapshotPrimeObservationValues;
};

