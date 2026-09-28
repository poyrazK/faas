/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Immutable terminal outcome of one task attempt.
 */
export type JobTaskAttemptResponse = {
  run_id: string;
  task_index: number;
  attempt: number;
  input_id?: string;
  input_ref?: string;
  status: 'succeeded' | 'failed' | 'timeout' | 'cancelled' | 'oom';
  instance_id?: string;
  error_class?: string;
  error_message?: string;
  exit_code?: number;
  started_at?: string;
  finished_at: string;
  log_content: string;
  log_truncated: boolean;
  output_manifest?: Record<string, any>;
};

