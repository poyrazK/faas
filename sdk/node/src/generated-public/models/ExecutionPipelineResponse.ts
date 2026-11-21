/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ExecutionPipelineCompletion } from './ExecutionPipelineCompletion.js';
import type { ExecutionPipelineProgress } from './ExecutionPipelineProgress.js';
import type { ExecutionPipelineRerun } from './ExecutionPipelineRerun.js';
import type { ExecutionPipelineStepResponse } from './ExecutionPipelineStepResponse.js';
/**
 * Managed task pipeline state, steps and available completion details.
 */
export type ExecutionPipelineResponse = {
  progress?: ExecutionPipelineProgress;
  rerun?: ExecutionPipelineRerun;
  completion?: ExecutionPipelineCompletion;
  deadline_at: string;
  cancel_requested?: boolean;
  steps: Array<ExecutionPipelineStepResponse>;
};

