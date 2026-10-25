/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationBusinessConditionResult } from './OperationBusinessConditionResult.js';
import type { OperationWorkflowConditionEvidence } from './OperationWorkflowConditionEvidence.js';
/**
 * Application condition snapshots matched by ID within the reason or selected path group on one pinned contract. Changed includes a changed result or description. Unreported means absent from an explicit current decision and does not prove resolution.
 */
export type OperationWorkflowConditionChange = {
  source: 'reason' | 'path';
  /**
   * Selected path group for path comparisons; omitted for reasons.
   */
  group_id?: string;
  condition_id: string;
  status: 'changed' | 'newly_reported' | 'unreported';
  before?: OperationBusinessConditionResult;
  after?: OperationBusinessConditionResult;
  previous_evidence?: OperationWorkflowConditionEvidence;
  current_evidence: OperationWorkflowConditionEvidence;
  explanation: string;
};

