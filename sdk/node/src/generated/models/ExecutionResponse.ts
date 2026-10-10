/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ExecutionArtifact } from './ExecutionArtifact.js';
import type { ExecutionFailure } from './ExecutionFailure.js';
import type { ExecutionUsage } from './ExecutionUsage.js';
import type { ResolvedExecutionLimits } from './ResolvedExecutionLimits.js';
/**
 * Disposable execution receipt. Runs-only keys can read only receipts
 * created by their key family; broad credentials retain account-wide
 * access. Source and input are intentionally omitted. A terminal response is written only after the
 * execution VM has been destroyed. Optional workflow metadata is echoed
 * as a grouping aid and remains subject to the same ownership boundary.
 *
 */
export type ExecutionResponse = {
  profile?: 'standard' | 'python-data-v1';
  /**
   * Scheduler-pinned base image digest, recorded before dispatch of a dependency profile.
   */
  runtime_image_digest?: string;
  /**
   * Immutable versions declared by the selected profile and verified by its guest before caller code runs.
   */
  packages?: Record<string, string>;
  id: string;
  /**
   * Caller-generated workflow grouping id, when assigned.
   */
  workflow_id?: string;
  /**
   * Optional step label, when assigned.
   */
  step_label?: string;
  status: 'queued' | 'restoring' | 'running' | 'succeeded' | 'failed' | 'timed_out' | 'out_of_memory' | 'cancelled';
  runtime: 'node22' | 'node24' | 'python312' | 'python313';
  limits: ResolvedExecutionLimits;
  /**
   * Selected output files, present only after successful execution and VM teardown.
   */
  artifacts?: Array<ExecutionArtifact>;
  /**
   * Terminal JSON result, omitted when unavailable.
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

