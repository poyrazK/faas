/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationSubject } from './OperationSubject.js';
import type { OperationWorkflowNewMissingConditionProgressChange } from './OperationWorkflowNewMissingConditionProgressChange.js';
import type { OperationWorkflowNewMissingConditionProgressCount } from './OperationWorkflowNewMissingConditionProgressCount.js';
import type { OperationWorkflowState } from './OperationWorkflowState.js';
import type { OperationWorkflowStateHistoryEntry } from './OperationWorkflowStateHistoryEntry.js';
/**
 * One current ongoing workflow with missing reports and a comparable retained baseline, ordered by stable workflow identity. Baseline_report identifies the pinned baseline definition source. A terminal baseline has no active expectations; newly active current gaps are new_expectation rather than reporting loss. Conditions use canonical current step and condition ID order. Customer projections omit tenant identifiers.
 */
export type OperationWorkflowNewMissingConditionProgressInstance = {
  platform_tenant_id?: string;
  subject: OperationSubject;
  baseline_report: OperationWorkflowStateHistoryEntry;
  baseline_terminal: boolean;
  state: OperationWorkflowState;
  state_changed: boolean;
  current_missing_condition_count: number;
  statuses: Array<OperationWorkflowNewMissingConditionProgressCount>;
  changes_truncated: boolean;
  changes: Array<OperationWorkflowNewMissingConditionProgressChange>;
};

