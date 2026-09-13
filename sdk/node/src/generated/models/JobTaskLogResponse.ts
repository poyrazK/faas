/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Durable per-task combined stdout/stderr tail. Truncated=true
 * means the tail was capped at MaxBytes; clients re-fetch
 * with a larger limit to see more.
 *
 */
export type JobTaskLogResponse = {
  task_status: 'queued' | 'claimed' | 'succeeded' | 'failed' | 'timeout' | 'cancelled' | 'oom';
  log_content: string;
  truncated: boolean;
  max_bytes: number;
};

