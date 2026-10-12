/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationSubject } from './OperationSubject.js';
import type { OperationWorkflowMissingConditionProgressChange } from './OperationWorkflowMissingConditionProgressChange.js';
import type { OperationWorkflowMissingConditionProgressCount } from './OperationWorkflowMissingConditionProgressCount.js';
import type { OperationWorkflowState } from './OperationWorkflowState.js';
import type { OperationWorkflowStateHistoryEntry } from './OperationWorkflowStateHistoryEntry.js';
/**
 * One workflow with baseline missing expectations, ordered by stable workflow identity. Baseline_report anchors the reporting Operation and publication time whose pinned definition declares the before expectations. State is current; state_changed is observational. Customer projections omit tenant identifiers. Changes use canonical baseline step and condition ID order.
 */
export type OperationWorkflowMissingConditionProgressInstance = {
  platform_tenant_id?: string;
  subject: OperationSubject;
  baseline_report: OperationWorkflowStateHistoryEntry;
  state: OperationWorkflowState;
  state_changed: boolean;
  baseline_missing_condition_count: number;
  statuses: Array<OperationWorkflowMissingConditionProgressCount>;
  changes_truncated: boolean;
  changes: Array<OperationWorkflowMissingConditionProgressChange>;
};

