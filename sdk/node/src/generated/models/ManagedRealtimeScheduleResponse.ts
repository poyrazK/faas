/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ManagedRealtimeScheduleRequest } from './ManagedRealtimeScheduleRequest.js';
export type ManagedRealtimeScheduleResponse = (ManagedRealtimeScheduleRequest & {
  initial_deliver_at: string;
  skipped_occurrences: number;
  skip_reason?: string;
  occurrence: number;
  completed_occurrences: number;
  /**
   * Lifetime recorded delivery attempts.
   */
  attempts: number;
  cycle_attempts: number;
  /**
   * Next pending retry time; omitted after completion or cancellation.
   */
  next_attempt_at?: string;
  last_attempt_at?: string;
  schedule_id: string;
  channel: string;
  version: number;
  status: 'pending' | 'paused' | 'published' | 'canceled' | 'failed' | 'skipped';
  /**
   * Committed history sequence when published.
   */
  sequence?: number;
  /**
   * Most recent recorded delivery failure code; retained after a successful retry.
   */
  last_error?: string;
  created_at: string;
  updated_at: string;
});

