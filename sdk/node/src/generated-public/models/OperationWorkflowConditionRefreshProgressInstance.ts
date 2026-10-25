/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationSubject } from './OperationSubject.js';
import type { OperationWorkflowConditionRefreshProgressChange } from './OperationWorkflowConditionRefreshProgressChange.js';
import type { OperationWorkflowConditionRefreshProgressCount } from './OperationWorkflowConditionRefreshProgressCount.js';
import type { OperationWorkflowState } from './OperationWorkflowState.js';
/**
 * One currently sampled workflow with overdue reporting evidence at the reconstructed baseline. Subject, current state and optional operator tenant context support workflow links. Customer projections omit tenant identifiers. Current terminal state does not imply reporting freshness or business verification success.
 */
export type OperationWorkflowConditionRefreshProgressInstance = {
  platform_tenant_id?: string;
  subject: OperationSubject;
  state: OperationWorkflowState;
  baseline_overdue_condition_count: number;
  newer_report_condition_count: number;
  statuses: Array<OperationWorkflowConditionRefreshProgressCount>;
  changes_truncated: boolean;
  /**
   * First 16 overdue baseline identities in canonical order with before and optional exact-identity after evidence; status counts remain exact.
   */
  changes: Array<OperationWorkflowConditionRefreshProgressChange>;
};

