/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowConditionCoverageInstance } from './OperationWorkflowConditionCoverageInstance.js';
import type { OperationWorkflowMissingConditionGroup } from './OperationWorkflowMissingConditionGroup.js';
/**
 * Reporting coverage for current states in the bounded ongoing sample. Expectations come only from the latest reporting Operation's immutable definition, not a global registry or every Operation in the workflow. Missing means no matching evidence in latest explicit contexts, not a failed check or proof it was never reported historically. Full continuous retained history is required. Conflicting results and over-limit evidence are excluded. Reported includes overdue, so expected equals reported plus missing and overdue is a subset of reported. Counts precede the per-workflow preview cap. No expectations is unconfigured coverage, not completeness.
 */
export type OperationWorkflowConditionCoverage = {
  /**
   * Missing reports grouped by current contract and state and pinned refresh owner. Workflow counts are unique per group; one workflow may appear for several owners. Canonical contract/state/owner ordering includes unassigned. Counts precede condition previews; group display caps do not change total missing conditions.
   */
  missing_groups: Array<OperationWorkflowMissingConditionGroup>;
  missing_groups_truncated: boolean;
  matching_workflow_count: number;
  sampled_workflow_count: number;
  compared_workflow_count: number;
  no_expectations_workflow_count: number;
  excluded_incomplete_workflow_count: number;
  excluded_ambiguous_workflow_count: number;
  excluded_condition_evidence_workflow_count: number;
  expected_condition_count: number;
  reported_condition_count: number;
  missing_condition_count: number;
  overdue_condition_count: number;
  cohort_truncated: boolean;
  items: Array<OperationWorkflowConditionCoverageInstance>;
};

