/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One recorded start of a database's compute.
 */
export type ManagedPostgresActivityComputeStart = {
  started_at: string;
  /**
   * Omitted while the compute runs or its suspend has not been read yet.
   */
  suspended_at?: string;
  cause: 'app_wake' | 'cron' | 'request' | 'app_start' | 'running_app' | 'external';
  app_id?: string;
  app_slug?: string;
  detail?: string;
};

