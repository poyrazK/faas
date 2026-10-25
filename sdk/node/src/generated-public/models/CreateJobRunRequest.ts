/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FailureRules } from './FailureRules.js';
import type { JobRunInput } from './JobRunInput.js';
/**
 * Atomic fan-out into indexed task records; supply `tasks`, an ordered
 * `inputs` array, or an external `input_manifest_uri` and checksum.
 * Each manifest entry is assigned to one task index in array order.
 * The handler validates the count against `Plan.JobMaxTasksPerRun`
 * (Hobby=100, Pro=1000, Scale=5000). Per-run overrides
 * (parallelism / retry_max / task_timeout_sec) inherit from
 * the job when null.
 *
 */
export type CreateJobRunRequest = {
  tasks?: number;
  failure_rules?: FailureRules;
  /**
   * Ordered input set. Creates one task per entry; tasks may be omitted or must match the input count. References are opaque and fetched by the customer image.
   */
  inputs?: Array<JobRunInput>;
  /**
   * obj://<app-id>/<bucket-id>/<key> for a JSON array of inputs, up to 16 MiB. Mutually exclusive with inputs.
   */
  input_manifest_uri?: string;
  /**
   * SHA-256 of the external manifest's exact bytes; required with input_manifest_uri.
   */
  input_manifest_sha256?: string;
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

