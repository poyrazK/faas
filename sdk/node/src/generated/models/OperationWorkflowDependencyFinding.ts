/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowDependency } from './OperationWorkflowDependency.js';
import type { OperationWorkflowState } from './OperationWorkflowState.js';
/**
 * Observed prerequisite-chain finding with a representative path starting at the selected workflow. Shared workflows are expanded once; this is not an exhaustive enumeration of every route or execution authorization.
 */
export type OperationWorkflowDependencyFinding = {
  kind: 'reported_blockers' | 'state_unknown' | 'outcome_unknown' | 'outcome_mismatch' | 'awaiting_application' | 'state_stale' | 'deadline_overdue' | 'cycle' | 'trace_limit';
  path: Array<OperationWorkflowDependency>;
  explanation: string;
  state?: OperationWorkflowState;
  limit?: 'depth' | 'workflows' | 'findings' | 'dependencies';
};

