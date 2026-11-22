/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { StoredWorkflowResponse } from './StoredWorkflowResponse.js';
/**
 * App stored workflow definitions, plan limit and execution availability.
 */
export type ListStoredWorkflowsResponse = {
  app_slug: string;
  runtime_enabled: boolean;
  unavailable_reason?: string;
  max_definitions: number;
  stored_workflows: Array<StoredWorkflowResponse>;
};

