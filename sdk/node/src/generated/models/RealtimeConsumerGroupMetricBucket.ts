/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Realtime Consumer Group Metric Bucket.
 */
export type RealtimeConsumerGroupMetricBucket = {
  bucket_at: string;
  first_claims: string;
  /**
   * Successful attempts with an observed processing start; legacy in-flight attempts are excluded.
   */
  completed: string;
  retries: string;
  dead_letters: string;
  queue_wait_milliseconds: number;
  processing_milliseconds: number;
};

