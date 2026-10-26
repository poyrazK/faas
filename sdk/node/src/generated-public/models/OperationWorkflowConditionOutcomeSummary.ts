/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowConditionOutcomeGroup } from './OperationWorkflowConditionOutcomeGroup.js';
/**
 * Observed condition/result associations with latest reported outcomes in the completed performance sample. Only complete histories with a reported outcome and at most 256 exact condition identities qualify. Reasons and selected-path decision facts at or before the first terminal report supply explicit results. Missing conditions are unreported; unknown is explicit. Groups overlap and do not establish causation.
 */
export type OperationWorkflowConditionOutcomeSummary = {
  /**
   * All completed matching workflows before bounded sampling.
   */
  matching_workflow_count: number;
  sampled_workflow_count: number;
  /**
   * Eligible sampled workflows with reported condition evidence; includes workflows whose groups are omitted by the display cap.
   */
  linked_workflow_count: number;
  /**
   * Eligible linked workflows reporting multiple results for at least one exact condition and rule identity; counted once per workflow.
   */
  changing_result_workflow_count: number;
  excluded_incomplete_workflow_count: number;
  missing_outcome_workflow_count: number;
  /**
   * Complete sampled workflows with an outcome but no reported conditions; omission is not unknown.
   */
  unclassified_workflow_count: number;
  /**
   * Complete workflows with an outcome exceeding 256 exact condition identities; excluded entirely before group display limits.
   */
  excluded_condition_evidence_workflow_count: number;
  cohort_truncated: boolean;
  groups_truncated: boolean;
  /**
   * First 64 identities in contract/decision/rule/version/condition ID/description/result/outcome lexical order. Counts of overlapping groups must not be summed.
   */
  groups: Array<OperationWorkflowConditionOutcomeGroup>;
};

