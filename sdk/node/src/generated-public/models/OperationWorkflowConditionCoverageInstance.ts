/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationSubject } from './OperationSubject.js';
import type { OperationWorkflowConditionCoverageEntry } from './OperationWorkflowConditionCoverageEntry.js';
import type { OperationWorkflowState } from './OperationWorkflowState.js';
/**
 * Coverage for one ongoing workflow. Conditions are ordered by declaring step then condition ID. Exact totals include preview omissions; workflows are ordered by stable identity.
 */
export type OperationWorkflowConditionCoverageInstance = {
  /**
   * Operator only; omitted in customer projections.
   */
  platform_tenant_id?: string;
  subject: OperationSubject;
  state: OperationWorkflowState;
  expected_condition_count: number;
  reported_condition_count: number;
  missing_condition_count: number;
  overdue_condition_count: number;
  conditions_truncated: boolean;
  conditions: Array<OperationWorkflowConditionCoverageEntry>;
};

