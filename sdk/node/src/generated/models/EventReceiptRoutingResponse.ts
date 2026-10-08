/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventRoutingRetryPolicy } from './EventRoutingRetryPolicy.js';
/**
 * Recipient routing checkpoint and lifetime attempts; execution attempts are separate.
 */
export type EventReceiptRoutingResponse = {
  /**
   * Captured wall-clock routing deadline.
   */
  delivery_deadline_at?: string;
  /**
   * Explicit operator override recorded for the current replay generation or backfill.
   */
  delivery_age_override?: boolean;
  routing_retry_policy?: EventRoutingRetryPolicy;
  /**
   * Why routing was filtered without consuming a routing attempt.
   */
  filter_reason?: 'schema_version_mismatch';
  /**
   * Why automatic routing retries stopped; failure_code retains the underlying cause.
   */
  retry_stop_reason?: 'non_retryable' | 'max_attempts' | 'max_duration' | 'delivery_expired';
  /**
   * Current replay generation duration budget spent on routing attempts and scheduled delays.
   */
  retry_spent_ms?: number;
  state: 'pending' | 'processing' | 'filtered' | 'enqueued' | 'failed';
  /**
   * Lifetime routing attempts, including replay generations.
   */
  attempts: number;
  /**
   * Capacity waits in this independent routing generation; subtract from generation_attempts for failure-budget use.
   */
  generation_capacity_deferrals?: number;
  /**
   * Lifetime capacity waits; excluded from the routing failure budget.
   */
  capacity_deferrals?: number;
  /**
   * Active capacity wait reason; absent after admission.
   */
  capacity_scope?: 'consumer' | 'app' | 'account';
  /**
   * Age since event acceptance while routing is pending or processing.
   */
  pending_age_seconds?: number;
  /**
   * Independent routing generation; absent on legacy whole-event receipts.
   */
  generation?: number;
  /**
   * Claims in the current independent generation, including capacity waits; subtract generation_capacity_deferrals for failure-budget use.
   */
  generation_attempts?: number;
  /**
   * Scheduled pending routing retry, not a dispatch guarantee.
   */
  next_attempt_at?: string;
  /**
   * Independent routing claim expiry; no claim token is exposed.
   */
  lease_until?: string;
  updated_at?: string;
  last_error?: string;
  failure_code?: string;
  retryable: boolean;
  replay_count: number;
  last_replayed_at?: string;
};

