/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ExecutionPipelineCompletedStep } from './ExecutionPipelineCompletedStep.js';
import type { ExecutionPipelineRerun } from './ExecutionPipelineRerun.js';
import type { ExecutionUsage } from './ExecutionUsage.js';
/**
 * Immutable v1 terminal manifest. Metadata and scoped references only; result and artifact bytes retain their ordinary Run receipt permissions and expiry. Completion identity is stable across delivery retries and redrive. Aggregate wall/CPU time sums every attempt; peak memory is the maximum attempt peak, not simultaneous residency or an invoice.
 */
export type ExecutionPipelineCompletion = {
  rerun?: ExecutionPipelineRerun;
  version: 'v1';
  completion_id: string;
  workflow_id: string;
  outcome: 'succeeded' | 'failed' | 'cancelled' | 'deadline_exceeded';
  finished_at: string;
  receipt: string;
  usage: ExecutionUsage;
  steps: Array<ExecutionPipelineCompletedStep>;
};

