/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowDurationDistribution } from './OperationWorkflowDurationDistribution.js';
/**
 * One latest explicitly reported result per exact condition identity and workflow. Elapsed and blocked durations cover the entire observed workflow to evaluated_at, not time under the condition. Evidence age is measured at the same time. Workflow state staleness follows its declared threshold and does not expire the condition.
 */
export type OperationWorkflowActiveConditionGroup = {
  within_budget_workflow_count: number;
  overdue_workflow_count: number;
  no_budget_workflow_count: number;
  contract_version: number;
  decision_code: string;
  rule_id: string;
  rule_version: string;
  condition_id: string;
  condition_description: string;
  condition_result: 'pass' | 'fail' | 'unknown';
  stale_workflow_count: number;
  elapsed_time: OperationWorkflowDurationDistribution;
  blocked_time: OperationWorkflowDurationDistribution;
  evidence_age: OperationWorkflowDurationDistribution;
};

