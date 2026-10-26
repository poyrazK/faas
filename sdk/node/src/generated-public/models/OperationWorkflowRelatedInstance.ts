/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowDependency } from './OperationWorkflowDependency.js';
import type { OperationWorkflowState } from './OperationWorkflowState.js';
/**
 * One-hop retained state resolution. Terminal without a required outcome does not establish business success.
 */
export type OperationWorkflowRelatedInstance = {
  dependency: OperationWorkflowDependency;
  status: 'unknown' | 'waiting' | 'terminal' | 'satisfied' | 'outcome_mismatch';
  state?: OperationWorkflowState;
};

