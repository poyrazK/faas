/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Per-task execution view. `status` is the closed
 * vocabulary (queued/claimed/running/succeeded/failed/
 * timeout/oom/cancelled). `instance_id` is the underlying
 * task VM id (instances.id) — null until schedd claims
 * the task. `error_class` + `error_message` are populated
 * when status is failed/timeout/oom.
 *
 */
export type JobTaskResponse = {
  task_index: number;
  status: 'queued' | 'claimed' | 'running' | 'succeeded' | 'failed' | 'timeout' | 'oom' | 'cancelled';
  attempt: number;
  instance_id?: string | null;
  error_class?: string | null;
  error_message?: string | null;
  started_at?: string | null;
  finished_at?: string | null;
  created_at: string;
};

