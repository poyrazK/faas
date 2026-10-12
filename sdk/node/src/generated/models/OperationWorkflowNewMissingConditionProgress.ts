/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowGapProgressGroup } from './OperationWorkflowGapProgressGroup.js';
import type { OperationWorkflowNewMissingConditionProgressCount } from './OperationWorkflowNewMissingConditionProgressCount.js';
import type { OperationWorkflowNewMissingConditionProgressInstance } from './OperationWorkflowNewMissingConditionProgressInstance.js';
/**
 * Optional baseline_token comparison explaining current missing reports relative to retained baseline expectations in today's completed and ongoing sample. Current terminal workflows produce no active gaps. Publication visibility, retained immutable baseline definitions, continuous baseline report history, conflict and evidence limits match missing_progress. Workflows with no retained baseline are not invented as new gaps, and unavailable definitions are excluded. This is not original cohort membership or total historical backlog. All current missing status counts precede preview caps; missing evidence is not failed verification.
 */
export type OperationWorkflowNewMissingConditionProgress = {
  groups: Array<OperationWorkflowGapProgressGroup>;
  groups_truncated: boolean;
  baseline_at: string;
  evaluated_at: string;
  sampled_workflow_count: number;
  compared_workflow_count: number;
  no_baseline_workflow_count: number;
  no_current_missing_workflow_count: number;
  excluded_incomplete_workflow_count: number;
  excluded_ambiguous_workflow_count: number;
  excluded_condition_evidence_workflow_count: number;
  excluded_definition_workflow_count: number;
  statuses: Array<OperationWorkflowNewMissingConditionProgressCount>;
  items: Array<OperationWorkflowNewMissingConditionProgressInstance>;
};

