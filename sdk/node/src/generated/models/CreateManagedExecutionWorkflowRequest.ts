/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CreateManagedExecutionWorkflowStep } from './CreateManagedExecutionWorkflowStep.js';
/**
 * Encrypted, server-managed DAG. Every step runs in its own disposable
 * Run. Independent steps may run in parallel up to max_parallel_steps
 * and the account's concurrent Run limit. The full JSON body must not
 * exceed 4 MiB. An account can have at most 16 active managed workflows.
 *
 */
export type CreateManagedExecutionWorkflowRequest = {
  workflow_id: string;
  version: string;
  /**
   * Maximum number of this workflow's Runs active at once; 0 uses the default of 1.
   */
  max_parallel_steps?: number;
  /**
   * Stop new admissions on a failed Run, or continue steps independent of failed branches.
   */
  failure_policy?: 'fail_fast' | 'continue_independent';
  steps: Array<CreateManagedExecutionWorkflowStep>;
};

