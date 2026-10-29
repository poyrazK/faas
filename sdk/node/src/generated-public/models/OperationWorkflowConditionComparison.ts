/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowConditionChange } from './OperationWorkflowConditionChange.js';
/**
 * Condition changes in explicit decision snapshots between consecutive retained revisions in the same tenant and workflow run and pinned contract. Shares history comparison statuses; missing decisions on metadata reports do not clear conditions. No causal inference.
 */
export type OperationWorkflowConditionComparison = {
  status: 'compared' | 'baseline_unavailable' | 'revision_gap' | 'contract_changed' | 'ambiguous_revision';
  /**
   * Reason conditions first then path groups then condition IDs in lexical order. Unchanged conditions are omitted. Contributor evidence carries changes for the entire qualifying report.
   */
  changes: Array<OperationWorkflowConditionChange>;
  /**
   * More condition changes exist than the 64 retained entries.
   */
  truncated: boolean;
};

