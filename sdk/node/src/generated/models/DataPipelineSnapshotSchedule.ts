/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Read-only full snapshot cadence. Zero row/byte budgets select the pilot ceilings of 100000 rows and 64 MiB. Runs are bounded to 45 seconds and retain the last successful capture on failure.
 */
export type DataPipelineSnapshotSchedule = {
  every_seconds: number;
  max_rows?: number;
  max_bytes?: number;
};

