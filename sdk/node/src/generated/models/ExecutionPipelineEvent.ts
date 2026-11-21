/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ExecutionPipelineStepProgress } from './ExecutionPipelineStepProgress.js';
export type ExecutionPipelineEvent = {
  sequence: number;
  type: 'pipeline' | 'step';
  status?: string;
  step?: ExecutionPipelineStepProgress;
  created_at: string;
};

