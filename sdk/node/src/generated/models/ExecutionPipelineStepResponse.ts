/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ExecutionPipelineAttemptResponse } from './ExecutionPipelineAttemptResponse.js';
import type { ExecutionTaskRef } from './ExecutionTaskRef.js';
/**
 * One declared task step with its dependencies and execution attempts.
 */
export type ExecutionPipelineStepResponse = {
  label: string;
  task?: ExecutionTaskRef;
  max_attempts: number;
  attempts: Array<ExecutionPipelineAttemptResponse>;
};

