/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimePreparationCounts } from './ObsRuntimePreparationCounts.js';
import type { ObsRuntimePreparationItem } from './ObsRuntimePreparationItem.js';
import type { ObsRuntimePreparationPhaseCounts } from './ObsRuntimePreparationPhaseCounts.js';
/**
 * Advisory durable image-preparation evidence for image workers in v2 profiles. Counts describe observation scopes, which may overlap across replicas. Positive phase evidence survives gaps in other components. No execution deadline or deployment success is inferred.
 */
export type ObsRuntimePreparationHealth = {
  state: 'waiting' | 'empty' | 'incomplete' | 'unknown';
  reason?: 'pending_preparations_observed' | 'incomplete_preparation_evidence' | 'preparation_evidence_unavailable';
  counts: ObsRuntimePreparationCounts;
  phases: Array<ObsRuntimePreparationPhaseCounts>;
  incomplete: boolean;
  items: Array<ObsRuntimePreparationItem>;
  omitted_items: number;
};

