/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * State counts partition all inspected items; expected, observed, unexpected and distinct_revisions overlap these counts. Unavailable jobs can have unknown expected counts; inspect jobs.
 */
export type ObsRuntimeBuildCounts = {
  expected: number;
  observed: number;
  matching: number;
  mismatched: number;
  missing: number;
  stale: number;
  unavailable: number;
  unexpected: number;
  distinct_revisions: number;
};

