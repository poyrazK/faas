/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Partial job update. nil pointer fields leave the column untouched.
 */
export type UpdateJobRequest = {
  image_ref?: string;
  command?: Array<string>;
  env_overrides?: Record<string, string>;
  ram_mb?: number;
  task_timeout_sec?: number;
  max_parallelism?: number;
  retry_max?: number;
  status?: 'active' | 'paused';
  /**
   * Replace the cron expression; an empty string removes the schedule.
   */
  schedule?: string;
  /**
   * Replace the schedule IANA timezone.
   */
  timezone?: string;
};

