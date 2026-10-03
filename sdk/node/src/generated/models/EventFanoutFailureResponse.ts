/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A terminal subscription routing failure before an invocation was created.
 */
export type EventFanoutFailureResponse = {
  event_id: string;
  event_source: string;
  event_type: string;
  subscription_id: string;
  state: 'failed';
  attempts: number;
  /**
   * Stable routing failure category; unknown covers failures recorded before classification was available. Optional for responses from older apid versions during rollout.
   */
  failure_code?: 'unknown' | 'invalid_subscription' | 'target_unavailable' | 'target_lookup_failed' | 'invocation_enqueue_failed' | 'internal_error';
  /**
   * True when the failure was caused by a transient lookup or enqueue error and replay may succeed without changing subscription configuration. Optional for responses from older apid versions during rollout.
   */
  retryable?: boolean;
  last_error: string;
  /**
   * When the event was accepted.
   */
  created_at: string;
  /**
   * When recipient routing became terminal.
   */
  failed_at: string;
};

