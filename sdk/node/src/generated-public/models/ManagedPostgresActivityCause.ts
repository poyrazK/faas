/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One cause's share of a period's starts and awake time.
 */
export type ManagedPostgresActivityCause = {
  cause: 'app_wake' | 'cron' | 'request' | 'app_start' | 'running_app' | 'external';
  app_id?: string;
  app_slug?: string;
  /**
   * The cron's path for a cron cause.
   */
  detail?: string;
  starts: number;
  awake_seconds: number;
  first_started_at: string;
};

