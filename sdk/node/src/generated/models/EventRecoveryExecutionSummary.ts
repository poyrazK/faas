/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Observations of admitted execution- or workflow-mode items. Both modes prefer the first saved terminal result, then live evidence of the exact admitted generation. Later native resumes report superseded only without an earlier saved terminal result. Missing evidence without a saved result is unknown. Native failed/dead runs count as failed. Counts sum to tracked_count and are separate from job admission state. Omitted for routing recovery. Saved results in both modes share recovery job retention.
 */
export type EventRecoveryExecutionSummary = {
  /**
   * Subset of tracked_count with saved confirmed terminal evidence. Not an additional state bucket.
   */
  saved_results: number;
  observed_at: string;
  tracked_count: number;
  queued: number;
  running: number;
  retrying: number;
  succeeded: number;
  failed: number;
  dead_lettered: number;
  expired: number;
  cancelled: number;
  superseded: number;
  unknown: number;
};

