/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeHandoffEvidenceReason } from './ObsRuntimeHandoffEvidenceReason.js';
import type { ObsRuntimePreparationPhaseName } from './ObsRuntimePreparationPhaseName.js';
import type { ObsRuntimePreparationPhaseValues } from './ObsRuntimePreparationPhaseValues.js';
/**
 * One ordered durable phase. Usable values require successful fresh collection after the evidence boundary and the same original scrape clock as collection metadata. Missing or expired snapshots never become empty.
 */
export type ObsRuntimePreparationPhase = {
  phase: ObsRuntimePreparationPhaseName;
  state: 'waiting' | 'empty' | 'missing' | 'stale' | 'conflicting' | 'unavailable';
  reason?: ObsRuntimeHandoffEvidenceReason;
  observed_at?: string;
  values?: ObsRuntimePreparationPhaseValues;
};

