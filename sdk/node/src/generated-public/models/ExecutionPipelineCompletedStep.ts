/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ExecutionPipelineCompletedAttempt } from './ExecutionPipelineCompletedAttempt.js';
import type { ExecutionTaskRef } from './ExecutionTaskRef.js';
/**
 * A terminal step outcome with attempts and retained output references.
 */
export type ExecutionPipelineCompletedStep = {
  reused_run_id?: string;
  output_sha256?: string;
  label: string;
  task?: ExecutionTaskRef;
  outcome: 'succeeded' | 'failed' | 'cancelled' | 'timed_out' | 'out_of_memory' | 'contract_failed' | 'blocked' | 'not_started';
  result_validation?: 'valid' | 'invalid';
  attempts: Array<ExecutionPipelineCompletedAttempt>;
};

