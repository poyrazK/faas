/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * State counts partition inspected target-loop pairs. Expected and observed counts overlap the partition; reported_stalls includes fresh stall reports with incomplete age evidence.
 */
export type ObsRuntimeLoopCounts = {
  expected: number;
  observed: number;
  no_reported_stall: number;
  stalled: number;
  missing: number;
  stale: number;
  conflicting: number;
  unavailable: number;
  reported_stalls: number;
  unexpected: number;
};

