/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Create an HTTP-path cron or deployment-attached app command schedule.
 */
export type CreateCronRequest = {
  /**
   * App id or slug.
   */
  app_id: string;
  schedule: string;
  /**
   * HTTP target path; defaults to / and is mutually exclusive with command.
   */
  path?: string;
  /**
   * Direct command argv; mutually exclusive with path.
   */
  command?: Array<string>;
  /**
   * Interpret one command string through the app shell when true.
   */
  command_shell?: boolean;
  /**
   * Per-fire command deadline; zero uses the 600-second default.
   */
  timeout_seconds?: number;
  /**
   * Combined stdout/stderr tail cap; zero uses the 1 MiB default.
   */
  max_output_bytes?: number;
  enabled?: boolean | null;
  /**
   * IANA timezone; defaults to UTC.
   */
  timezone?: string;
  /**
   * Skip a scheduled fire when an earlier cron invocation is still running.
   */
  skip_if_running?: boolean | null;
  /**
   * Additional command attempts after failure or timeout; command crons only.
   */
  retry_max?: number;
  /**
   * Base retry delay in seconds; doubles per retry and is capped at 24 hours. Command crons only.
   */
  retry_backoff_seconds?: number;
};

