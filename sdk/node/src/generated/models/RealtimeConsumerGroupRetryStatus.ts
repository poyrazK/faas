/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Realtime Consumer Group Retry Status.
 */
export type RealtimeConsumerGroupRetryStatus = {
  enabled: boolean;
  base_delay_seconds: number;
  max_delay_seconds: number;
  jitter: boolean;
  permanent_failure_codes: Array<string>;
  /**
   * Earliest future available_at among ready snapshots; null when none.
   */
  next_retry_at: string | null;
};

