/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A cron trigger with an optional IANA timezone and overlap policy.
 */
export type CronResponse = {
  id: string;
  app_id: string;
  schedule: string;
  path: string;
  enabled: boolean;
  /**
   * IANA timezone used to evaluate the schedule; defaults to UTC.
   */
  timezone: string;
  /**
   * When true, consume a scheduled occurrence while a prior cron invocation is pending or dispatching.
   */
  skip_if_running: boolean;
  created_at: string;
  last_fired_at?: string | null;
};

