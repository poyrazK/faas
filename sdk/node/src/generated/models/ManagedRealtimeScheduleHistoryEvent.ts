/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Recorded execution or lifecycle event for a channel publish schedule.
 */
export type ManagedRealtimeScheduleHistoryEvent = {
  skipped_occurrences: number;
  skip_reason?: string;
  occurrence: number;
  completed_occurrences: number;
  version: number;
  event: 'baseline' | 'created' | 'rescheduled' | 'canceled' | 'manual_retry' | 'attempt_failed' | 'published' | 'paused' | 'resumed' | 'skipped';
  status: 'pending' | 'paused' | 'published' | 'canceled' | 'failed' | 'skipped';
  attempts: number;
  cycle_attempts: number;
  deliver_at: string;
  next_attempt_at?: string;
  failure_code?: string;
  sequence?: number;
  occurred_at: string;
};

