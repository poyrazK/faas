/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Sample states partition current inspected targets. Comparison counts cover fresh samples; removed targets belong to the prior observation and overlap no current partition.
 */
export type ObsRuntimeProcessCounts = {
  fresh: number;
  missing: number;
  stale: number;
  conflicting: number;
  unavailable: number;
  compared: number;
  changed: number;
  unchanged: number;
  same_revision_changes: number;
  different_revision_changes: number;
  unknown_revision_changes: number;
  new_targets: number;
  removed_targets: number;
  repeated_samples: number;
  comparison_unavailable: number;
};

