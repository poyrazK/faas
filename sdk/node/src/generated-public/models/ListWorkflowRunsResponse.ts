/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowRunResponse } from './WorkflowRunResponse.js';
/**
 * A paginated list of durable workflow runs.
 */
export type ListWorkflowRunsResponse = {
  runs: Array<WorkflowRunResponse>;
  total: number;
};

