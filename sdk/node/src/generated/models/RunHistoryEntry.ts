/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ExecutionTaskRef } from './ExecutionTaskRef.js';
import type { ExecutionUsage } from './ExecutionUsage.js';
import type { RunAttribution } from './RunAttribution.js';
/**
 * One invocation's attribution, status and usage without its payload.
 */
export type RunHistoryEntry = {
  result_validation_status?: 'valid' | 'invalid';
  id: string;
  task?: ExecutionTaskRef;
  attribution?: RunAttribution;
  status: 'queued' | 'restoring' | 'running' | 'succeeded' | 'failed' | 'timed_out' | 'out_of_memory' | 'cancelled';
  runtime: 'node22' | 'node24' | 'python312' | 'python313';
  created_at: string;
  started_at?: string;
  finished_at?: string;
  elapsed_ms: number;
  usage?: ExecutionUsage;
  failure_code?: string;
  receipt_url: string;
};

