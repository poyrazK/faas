/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A suggestion for a cause that keeps the database awake.
 */
export type ManagedPostgresActivityHint = {
  code: 'cron_wakes_database' | 'app_wake_starts_database' | 'app_start_wakes_database' | 'running_app_holds_connections' | 'external_client';
  cause: string;
  app_slug?: string;
  message: string;
  /**
   * The CLI command that acts on the hint, when there is one.
   */
  command?: string;
};

