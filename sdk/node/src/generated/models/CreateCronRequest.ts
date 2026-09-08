/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Cron creation payload: schedule expression, target URL, and optional timezone/overlap policy.
 */
export type CreateCronRequest = {
  app_id: string;
  schedule: string;
  path?: string;
  enabled?: boolean | null;
  /**
   * IANA timezone; defaults to UTC.
   */
  timezone?: string;
  /**
   * Skip a scheduled fire when an earlier cron invocation is still running.
   */
  skip_if_running?: boolean | null;
};

