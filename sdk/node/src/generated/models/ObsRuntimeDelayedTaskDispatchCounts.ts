/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Full-set signal coverage before preview clipping, with four counters per scheduler target. Increased, unchanged, baseline, missing, stale, conflicting and unavailable partition inspected signals. Observed counts usable current points, including unknown comparisons. These are signal classifications, not delta magnitudes or unique tasks.
 */
export type ObsRuntimeDelayedTaskDispatchCounts = {
  expected: number;
  observed: number;
  increased: number;
  unchanged: number;
  baseline: number;
  missing: number;
  stale: number;
  conflicting: number;
  unavailable: number;
  unexpected: number;
};

