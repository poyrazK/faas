/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ExecutionFailure } from './ExecutionFailure.js';
import type { ExecutionUsage } from './ExecutionUsage.js';
import type { ResolvedExecutionLimits } from './ResolvedExecutionLimits.js';
/**
 * Account-scoped disposable execution receipt. Source and input are
 * intentionally omitted. A terminal response is written only after the
 * execution VM has been destroyed.
 *
 */
export type ExecutionResponse = {
  id: string;
  status: 'queued' | 'restoring' | 'running' | 'succeeded' | 'failed' | 'timed_out' | 'out_of_memory' | 'cancelled';
  runtime: 'node22' | 'node24' | 'python312' | 'python313';
  limits: ResolvedExecutionLimits;
  /**
   * Terminal JSON result
   */
  result?: any;
  stdout?: string;
  stderr?: string;
  output_truncated: boolean;
  exit_code?: number | null;
  usage?: (ExecutionUsage | null);
  failure?: (ExecutionFailure | null);
  created_at: string;
  started_at?: string | null;
  finished_at?: string | null;
};

