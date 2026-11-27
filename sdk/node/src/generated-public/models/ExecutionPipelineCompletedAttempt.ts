/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ExecutionPipelineArtifactRef } from './ExecutionPipelineArtifactRef.js';
import type { ExecutionUsage } from './ExecutionUsage.js';
/**
 * Recorded final status and timing for one attempt in a completed pipeline.
 */
export type ExecutionPipelineCompletedAttempt = {
  run_id: string;
  attempt: number;
  status: 'succeeded' | 'failed' | 'timed_out' | 'out_of_memory' | 'cancelled';
  failure_code?: string;
  receipt: string;
  usage: ExecutionUsage;
  artifacts: Array<ExecutionPipelineArtifactRef>;
};

