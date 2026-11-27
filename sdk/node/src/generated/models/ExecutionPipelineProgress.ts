/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ExecutionPipelineStepProgress } from './ExecutionPipelineStepProgress.js';
/**
 * Current pipeline sequence and observed progress for each step.
 */
export type ExecutionPipelineProgress = {
  status: 'queued' | 'running' | 'succeeded' | 'failed' | 'cancelled' | 'deadline_exceeded';
  steps: Array<ExecutionPipelineStepProgress>;
};

