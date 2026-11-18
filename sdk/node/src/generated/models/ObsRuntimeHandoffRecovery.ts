/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeHandoffEvidenceReason } from './ObsRuntimeHandoffEvidenceReason.js';
import type { ObsRuntimeHandoffRecoveryValues } from './ObsRuntimeHandoffRecoveryValues.js';
/**
 * Sampled recovery-pass activity with no new progress timeout. Synchronous image work can legitimately occupy a long pass; stopping can retain an in-flight pass and does not prove drained work.
 */
export type ObsRuntimeHandoffRecovery = {
  state: 'waiting' | 'working' | 'stopping' | 'not_started' | 'missing' | 'stale' | 'conflicting' | 'unavailable';
  reason?: ObsRuntimeHandoffEvidenceReason;
  observed_at?: string;
  values?: ObsRuntimeHandoffRecoveryValues;
};

