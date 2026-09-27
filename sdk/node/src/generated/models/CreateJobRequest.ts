/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Job creation payload — name + image + command + caps; schedule enables recurring runs.
 */
export type CreateJobRequest = {
  name: string;
  kind?: 'batch' | 'recurring';
  /**
   * Optional five-field cron expression. When present, the job becomes recurring and schedd creates one single-task run per occurrence.
   */
  schedule?: string;
  /**
   * IANA timezone for schedule; defaults to UTC.
   */
  timezone?: string;
  image_ref: string;
  command: Array<string>;
  env_overrides?: Record<string, string>;
  ram_mb?: number;
  task_timeout_sec?: number;
  max_parallelism?: number;
  retry_max?: number;
};

