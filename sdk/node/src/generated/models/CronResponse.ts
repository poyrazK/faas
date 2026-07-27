/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A cron trigger: schedule (cron expression), target URL, last/next run timestamps, and enabled flag.
 */
export type CronResponse = {
  id: string;
  app_id: string;
  schedule: string;
  path: string;
  enabled: boolean;
  created_at: string;
  last_fired_at?: string | null;
};

