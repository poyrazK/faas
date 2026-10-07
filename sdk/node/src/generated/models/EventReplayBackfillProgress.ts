/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Durable scan outcomes and current routing state for a backfill job.
 */
export type EventReplayBackfillProgress = {
  scanned: number;
  matched: number;
  filtered: number;
  pending: number;
  processing: number;
  /**
   * Routing admitted an invocation; handler completion is separate.
   */
  enqueued: number;
  failed: number;
  /**
   * Failed recipient deliveries eligible for the retry-failed operation.
   */
  retryable_failed: number;
  skipped_captured: number;
  skipped_unknown: number;
  skipped_existing: number;
  skipped_unsettled: number;
};

