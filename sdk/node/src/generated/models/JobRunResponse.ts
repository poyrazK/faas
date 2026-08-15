/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Read-only view of one run. `aggregate_status` rolls up
 * the per-task statuses (queued/running/succeeded/failed/
 * cancelled). `tasks_*` counters are the per-task fan-in
 * the dispatch tick writes. `started_at`/`finished_at`
 * are omitted while NULL.
 *
 */
export type JobRunResponse = {
  id: string;
  job_id: string;
  trigger_kind: 'manual' | 'scheduled' | 'webhook';
  tasks: number;
  parallelism: number;
  env_overrides?: Record<string, string>;
  aggregate_status: 'queued' | 'running' | 'succeeded' | 'failed' | 'cancelled';
  tasks_succeeded: number;
  tasks_failed: number;
  tasks_cancelled: number;
  tasks_running: number;
  started_at?: string | null;
  finished_at?: string | null;
  created_at: string;
};

