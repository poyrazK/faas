/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ExecutionWorkflowStatusCounts } from './ExecutionWorkflowStatusCounts.js';
import type { ExecutionWorkflowUsage } from './ExecutionWorkflowUsage.js';
import type { ManagedExecutionWorkflowResponse } from './ManagedExecutionWorkflowResponse.js';
/**
 * Workflow lifecycle and usage aggregate visible to the authenticated principal.
 */
export type ExecutionWorkflowResponse = {
  workflow_id: string;
  run_count: number;
  status_counts: ExecutionWorkflowStatusCounts;
  usage: ExecutionWorkflowUsage;
  /**
   * Managed plans matching the workflow id and visible to this principal. Broad credentials can see plans submitted by multiple key families.
   */
  managed?: Array<ManagedExecutionWorkflowResponse>;
};

