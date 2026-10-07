/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventReplayBackfillJobResponse } from './EventReplayBackfillJobResponse.js';
/**
 * Number of failed deliveries retried and the resulting job status.
 */
export type EventReplayBackfillRetryResponse = {
  retried_count: number;
  /**
   * Failed routing items that can still be requeued from this job.
   */
  remaining_retryable_count: number;
  job: EventReplayBackfillJobResponse;
};

