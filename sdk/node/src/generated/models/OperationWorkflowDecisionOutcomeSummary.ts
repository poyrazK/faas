/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowDecisionOutcomeGroup } from './OperationWorkflowDecisionOutcomeGroup.js';
import type { OperationWorkflowRuleComparisonSummary } from './OperationWorkflowRuleComparisonSummary.js';
/**
 * Observed decision and path associations with latest reported outcomes in the completed performance cohort. Only complete histories with a reported outcome qualify. Repeated decision or path identities count once per workflow and group. Groups overlap; their counts must not be summed. Classification uses reasons and selections reported at or before the first terminal report. Sequence does not establish causation. Groups retain the first 64 canonical identities in ascending order.
 */
export type OperationWorkflowDecisionOutcomeSummary = {
  rule_comparisons?: OperationWorkflowRuleComparisonSummary;
  /**
   * All matching completed workflows before bounded sampling.
   */
  matching_workflow_count: number;
  sampled_workflow_count: number;
  /**
   * Sampled complete workflows with a reported outcome and at least one decision or path identity. Includes workflows whose groups are omitted by the group limit.
   */
  linked_workflow_count: number;
  excluded_incomplete_workflow_count: number;
  /**
   * Complete completed workflows without a latest reported outcome code.
   */
  missing_outcome_workflow_count: number;
  /**
   * Complete completed workflows with an outcome but no qualifying decision or path.
   */
  unclassified_workflow_count: number;
  cohort_truncated: boolean;
  groups_truncated: boolean;
  groups: Array<OperationWorkflowDecisionOutcomeGroup>;
};

