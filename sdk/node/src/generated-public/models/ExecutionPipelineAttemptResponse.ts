/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Runtime state and linked Run receipt for a pipeline attempt.
 */
export type ExecutionPipelineAttemptResponse = {
  run_id: string;
  attempt: number;
  previous_run_id?: string;
  status: 'queued' | 'restoring' | 'running' | 'succeeded' | 'failed' | 'timed_out' | 'out_of_memory' | 'cancelled';
  failure_code?: string;
};

