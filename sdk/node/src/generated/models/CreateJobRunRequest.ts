/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { JobRunInput } from './JobRunInput.js';
/**
 * Atomic fan-out into indexed task records; supply either `tasks` or
 * an ordered `inputs` array. The handler validates the count against `Plan.JobMaxTasksPerRun`
 * (Hobby=100, Pro=1000, Scale=5000). Per-run overrides
 * (parallelism / retry_max / task_timeout_sec) inherit from
 * the job when null.
 *
 */
export type CreateJobRunRequest = {
  tasks?: number;
  /**
   * Ordered input set. Creates one task per entry; tasks may be omitted or must match the input count. References are opaque and fetched by the customer image.
   */
  inputs?: Array<JobRunInput>;
  execution_class?: 'standard' | 'flexible';
  failure_policy?: 'continue' | 'fail_fast';
  /**
   * Earliest admission time for flexible tasks; defaults to now.
   */
  eligible_at?: string;
  /**
   * Required for flexible runs. Unstarted tasks expire at this time; completion is not guaranteed by this time.
   */
  latest_start_at?: string;
  parallelism?: number;
  retry_max?: number;
  task_timeout_sec?: number;
  env_overrides?: Record<string, string>;
  /**
   * Replace the command arguments for this run, retaining its executable. Empty array removes trailing arguments.
   */
  arguments?: Array<string>;
};

