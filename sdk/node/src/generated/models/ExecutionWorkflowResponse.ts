/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ExecutionWorkflowStatusCounts } from './ExecutionWorkflowStatusCounts.js';
import type { ExecutionWorkflowUsage } from './ExecutionWorkflowUsage.js';
/**
 * Workflow lifecycle and usage aggregate visible to the authenticated principal.
 */
export type ExecutionWorkflowResponse = {
  workflow_id: string;
  run_count: number;
  status_counts: ExecutionWorkflowStatusCounts;
  usage: ExecutionWorkflowUsage;
};

