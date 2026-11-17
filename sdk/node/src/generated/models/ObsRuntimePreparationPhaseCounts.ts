/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimePreparationPhaseName } from './ObsRuntimePreparationPhaseName.js';
/**
 * Observation scope counts for one closed phase, retained over the complete inspected set before preview clipping. These are not checkpoint totals or completion counters.
 */
export type ObsRuntimePreparationPhaseCounts = {
  phase: ObsRuntimePreparationPhaseName;
  waiting_scopes: number;
  empty_scopes: number;
  unknown_age_scopes: number;
};

