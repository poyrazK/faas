/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Immutable routing outcome or explicit operator replay request for one event recipient.
 */
export type EventFanoutAttemptResponse = {
  /**
   * Why routing was filtered without consuming a routing attempt.
   */
  filter_reason?: 'schema_version_mismatch';
  /**
   * Why automatic routing retries stopped; failure_code retains the underlying cause.
   */
  retry_stop_reason?: 'non_retryable' | 'max_attempts' | 'max_duration' | 'delivery_expired';
  subscription_id: string;
  action: 'fanout_attempt' | 'operator_replay';
  state: 'pending' | 'filtered' | 'enqueued' | 'failed';
  attempt_number: number;
  failure_code?: 'unknown' | 'invalid_subscription' | 'target_unavailable' | 'target_lookup_failed' | 'invocation_enqueue_failed' | 'internal_error';
  retryable: boolean;
  last_error?: string;
  occurred_at: string;
  /**
   * Capacity wait scope for this immutable observation.
   */
  capacity_scope?: 'consumer' | 'app' | 'account';
  /**
   * Cumulative deferrals at this observation; summaries provide the current total.
   */
  capacity_deferrals?: number;
  /**
   * Error or failure code detail was truncated to its UTF-8 byte ceiling.
   */
  details_truncated?: boolean;
};

