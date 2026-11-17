/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeSnapshotPrimeClassName } from './ObsRuntimeSnapshotPrimeClassName.js';
/**
 * Observation scope counts for one closed class across the full inspected set before clipping. These are neither unique attempt totals nor completion counters.
 */
export type ObsRuntimeSnapshotPrimeClassCounts = {
  class: ObsRuntimeSnapshotPrimeClassName;
  nonempty_scopes: number;
  empty_scopes: number;
  unknown_age_scopes: number;
};

