/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Durable rolling restart handoff for a pending PostgreSQL or object-storage rotation. Completion does not imply fresh resident instances or successful verification. Not_queued means no retained outbox record was found; unknown means progress could not be read.
 */
export type BindingRefresh = {
  wake_id: string;
  status: 'queued' | 'retrying' | 'running' | 'completed' | 'failed' | 'not_queued' | 'unknown';
  attempts: number;
  failure_reason?: 'telemetry_missing' | 'requests_active' | 'quiet_period_not_elapsed' | 'restart_attempt_failed';
  requested_at?: string;
  completed_at?: string;
};

