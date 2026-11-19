/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeBuildRecoveryCleanupValues } from './ObsRuntimeBuildRecoveryCleanupValues.js';
import type { ObsRuntimeHandoffEvidenceReason } from './ObsRuntimeHandoffEvidenceReason.js';
/**
 * Durable outstanding VM cleanup obligations, including live claims and retries. Usable values require successful fresh collection and coherent source clocks. Nonempty evidence is advisory and does not prove a VM is still alive.
 */
export type ObsRuntimeBuildRecoveryCleanup = {
  state: 'nonempty' | 'empty' | 'missing' | 'stale' | 'conflicting' | 'unavailable';
  reason?: ObsRuntimeHandoffEvidenceReason;
  observed_at?: string;
  values?: ObsRuntimeBuildRecoveryCleanupValues;
};

