/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeBuildRecoveryRunningValues } from './ObsRuntimeBuildRecoveryRunningValues.js';
import type { ObsRuntimeHandoffEvidenceReason } from './ObsRuntimeHandoffEvidenceReason.js';
/**
 * Running build-claim evidence requires fresh successful collection after the evidence boundary and coherent original scrape clocks. Missing or failed collection never becomes an empty set.
 */
export type ObsRuntimeBuildRecoveryRunning = {
  state: 'nonempty' | 'empty' | 'missing' | 'stale' | 'conflicting' | 'unavailable';
  reason?: ObsRuntimeHandoffEvidenceReason;
  observed_at?: string;
  values?: ObsRuntimeBuildRecoveryRunningValues;
};

