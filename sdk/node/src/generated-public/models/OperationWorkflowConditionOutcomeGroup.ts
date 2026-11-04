/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowDurationDistribution } from './OperationWorkflowDurationDistribution.js';
/**
 * A workflow counts once per exact contract and decision and rule/version and condition ID/description and observed result and outcome. Different descriptions remain separate. Multiple observed results cause overlapping memberships. Durations cover the full completed workflow rather than time under this condition; no causal claim.
 */
export type OperationWorkflowConditionOutcomeGroup = {
  contract_version: number;
  decision_code: string;
  rule_id: string;
  rule_version: string;
  condition_id: string;
  /**
   * Public condition vocabulary included in the exact grouping identity; UTF-8 byte bound without controls.
   */
  condition_description: string;
  condition_result: 'pass' | 'fail' | 'unknown';
  outcome_code: string;
  /**
   * Contributors reporting more than one result for this exact condition identity before or at the first terminal report.
   */
  changing_result_workflow_count: number;
  completion_time: OperationWorkflowDurationDistribution;
  blocked_time: OperationWorkflowDurationDistribution;
};

