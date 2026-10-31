/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowMissingConditionProgressCount } from './OperationWorkflowMissingConditionProgressCount.js';
import type { OperationWorkflowMissingConditionProgressInstance } from './OperationWorkflowMissingConditionProgressInstance.js';
/**
 * Optional baseline_token comparison of missing expectations reconstructed from retained reports and the baseline reporting Operation's immutable definition in today's completed and ongoing samples. Publication timestamps determine baseline visibility. This does not store original cohort membership or prove backlog resolution. Missing historical definitions, incomplete report history, conflicts and evidence limits are explicitly excluded. Baseline histories must be continuous within one contract; current contract boundaries permit contract_changed classification. Prior terminal reports do not create baseline gaps. Counts precede change preview limits. Conditions leaving the current state's expectation set, changed declarations and terminal workflows do not imply successful verification.
 */
export type OperationWorkflowMissingConditionProgress = {
  baseline_at: string;
  evaluated_at: string;
  sampled_workflow_count: number;
  compared_workflow_count: number;
  no_baseline_workflow_count: number;
  no_missing_baseline_workflow_count: number;
  excluded_incomplete_workflow_count: number;
  excluded_ambiguous_workflow_count: number;
  excluded_condition_evidence_workflow_count: number;
  excluded_definition_workflow_count: number;
  statuses: Array<OperationWorkflowMissingConditionProgressCount>;
  items: Array<OperationWorkflowMissingConditionProgressInstance>;
};

