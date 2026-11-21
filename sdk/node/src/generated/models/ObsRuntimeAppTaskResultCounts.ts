/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Full-set signal coverage before clipping. Each scheduler contributes 15 completion kind/status signals and three recovery signals. Increased, unchanged, baseline, missing, stale, conflicting and unavailable partition inspected signals. Observed counts usable current points, including unknown comparisons. These counts describe signals, not cumulative counters, delta magnitudes or unique tasks.
 */
export type ObsRuntimeAppTaskResultCounts = {
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

