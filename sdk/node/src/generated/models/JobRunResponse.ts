/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Wire projection of state.JobRun. Aggregate counters are recomputed by schedd after every terminal task transition.
 */
export type JobRunResponse = {
  id: string;
  job_id: string;
  account_id: string;
  trigger_kind: 'manual' | 'scheduled' | 'triggered';
  env_overrides?: Record<string, string>;
  tasks: number;
  /**
   * 0 for numeric fan-out, 1 for the ordered inline input manifest.
   */
  input_manifest_version?: number;
  /**
   * SHA-256 of the canonical ordered input manifest.
   */
  input_digest?: string;
  parallelism: number;
  execution_class: 'standard' | 'flexible';
  failure_policy: 'continue' | 'fail_fast';
  eligible_at?: string;
  latest_start_at?: string;
  retry_max?: number;
  task_timeout_sec?: number;
  /**
   * Command captured at run creation.
   */
  command?: Array<string>;
  aggregate_status: 'queued' | 'running' | 'succeeded' | 'failed' | 'cancelled' | 'dead_letter';
  tasks_succeeded: number;
  tasks_failed: number;
  tasks_cancelled: number;
  tasks_running: number;
  dead_letter_count: number;
  started_at?: string;
  finished_at?: string;
  created_at: string;
};

