/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * An app schedule: either an HTTP-path cron or a deployment-attached command cron.
 */
export type CronResponse = {
  id: string;
  app_id: string;
  /**
   * HTTP triggers the app path; command runs argv in a fresh VM using the live deployment selected at fire time.
   */
  kind: 'http' | 'command';
  schedule: string;
  /**
   * HTTP target path; omitted for command crons.
   */
  path?: string;
  /**
   * Direct command argv; present only for command crons.
   */
  command?: Array<string>;
  /**
   * Interpret a one-element command through the app shell; false executes argv directly.
   */
  command_shell?: boolean;
  /**
   * Per-fire command deadline.
   */
  timeout_seconds?: number;
  /**
   * Combined stdout/stderr tail cap for command runs.
   */
  max_output_bytes?: number;
  /**
   * Additional attempts after an execution fails or times out; zero disables retries.
   */
  retry_max?: number;
  /**
   * Base retry delay. Each subsequent retry doubles the delay, capped at 24 hours.
   */
  retry_backoff_seconds?: number;
  enabled: boolean;
  /**
   * Why an enabled schedule is paused. Redeploy the app successfully to clear no_live_deployment.
   */
  suspended_reason?: 'no_live_deployment';
  /**
   * IANA timezone used to evaluate the schedule; defaults to UTC.
   */
  timezone: string;
  /**
   * When true, consume a scheduled occurrence while a prior HTTP invocation or app command task is active.
   */
  skip_if_running: boolean;
  created_at: string;
  last_fired_at?: string | null;
};

