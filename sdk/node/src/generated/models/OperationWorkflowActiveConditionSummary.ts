/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowActiveConditionGroup } from './OperationWorkflowActiveConditionGroup.js';
import type { OperationWorkflowConditionRefreshOwnerGroup } from './OperationWorkflowConditionRefreshOwnerGroup.js';
/**
 * Latest explicit condition snapshots in the ongoing sample with complete retained history. The latest reason and latest nonempty selected-path snapshot provide independent contexts; a path without a fact clears that path context. Metadata-only reports preserve evidence. Explicit decision omission removes older conditions. Contract changes reset contexts. Conflicting results for an exact identity exclude the workflow. Missing conditions remain unreported. At most 256 contexts and 256 final condition identities per workflow qualify. Groups overlap; no present business truth or causation is inferred.
 */
export type OperationWorkflowActiveConditionSummary = {
  /**
   * Eligible sampled workflows with at least one overdue selected condition report; counted once before owner display limits.
   */
  overdue_workflow_count: number;
  /**
   * Exact selected overdue condition identities across eligible sampled workflows before owner display limits.
   */
  overdue_condition_count: number;
  refresh_owners_truncated: boolean;
  /**
   * First 64 owner strings in lexical order; unassigned sorts first. Counts for returned owners are exact. Workflows may overlap across owners.
   */
  refresh_owners: Array<OperationWorkflowConditionRefreshOwnerGroup>;
  matching_workflow_count: number;
  sampled_workflow_count: number;
  linked_workflow_count: number;
  excluded_incomplete_workflow_count: number;
  excluded_ambiguous_workflow_count: number;
  excluded_condition_evidence_workflow_count: number;
  unclassified_workflow_count: number;
  cohort_truncated: boolean;
  groups_truncated: boolean;
  /**
   * First 64 contract/decision/rule/version/condition ID/description/result identities in lexical order with exact counts for returned groups.
   */
  groups: Array<OperationWorkflowActiveConditionGroup>;
};

