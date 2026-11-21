/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ExecutionPipelineAttemptResponse } from './ExecutionPipelineAttemptResponse.js';
import type { ExecutionTaskRef } from './ExecutionTaskRef.js';
export type ExecutionPipelineStepResponse = {
  label: string;
  task?: ExecutionTaskRef;
  max_attempts: number;
  attempts: Array<ExecutionPipelineAttemptResponse>;
};

