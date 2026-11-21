/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeHandoffBacklogValues } from './ObsRuntimeHandoffBacklogValues.js';
import type { ObsRuntimeHandoffEvidenceReason } from './ObsRuntimeHandoffEvidenceReason.js';
/**
 * Waiting or known-empty snapshot evidence requires successful fresh collection after the evidence boundary and coherent original sample clocks. Missing evidence never becomes zero work.
 */
export type ObsRuntimeHandoffBacklog = {
  state: 'waiting' | 'empty' | 'missing' | 'stale' | 'conflicting' | 'unavailable';
  reason?: ObsRuntimeHandoffEvidenceReason;
  observed_at?: string;
  values?: ObsRuntimeHandoffBacklogValues;
};

