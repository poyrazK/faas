/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Durable status for an accepted fresh runtime-configuration restart.
 */
export type RuntimeConfigRestartStatusResponse = {
  wake_id: string;
  status: 'queued' | 'running' | 'retrying' | 'completed' | 'failed';
  /**
   * Number of scheduler handling attempts so far.
   */
  attempts: number;
  /**
   * Stable safe reason from the most recent unsuccessful attempt, when available.
   */
  failure_reason?: 'telemetry_missing' | 'requests_active' | 'quiet_period_not_elapsed' | 'restart_attempt_failed';
  requested_at: string;
  completed_at?: string;
};

