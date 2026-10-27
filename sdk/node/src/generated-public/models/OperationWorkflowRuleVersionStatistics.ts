/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowDurationDistribution } from './OperationWorkflowDurationDistribution.js';
import type { OperationWorkflowRuleVersionCohort } from './OperationWorkflowRuleVersionCohort.js';
import type { OperationWorkflowRuleVersionOutcome } from './OperationWorkflowRuleVersionOutcome.js';
/**
 * Each sampled workflow counts once per observed contract/decision/rule/version identity. Observed membership uses retained reports up to the first retained terminal report; incomplete histories supply coverage counts only. Eligible workflows require complete history and a latest reported outcome. Duration distributions cover the full completed workflow. Outcome proportions use all eligible workflows for this version as denominator, including omitted outcome groups.
 */
export type OperationWorkflowRuleVersionStatistics = {
  single_version?: OperationWorkflowRuleVersionCohort;
  rule_version: string;
  observed_workflow_count: number;
  eligible_workflow_count: number;
  excluded_incomplete_workflow_count: number;
  missing_outcome_workflow_count: number;
  completion_time: OperationWorkflowDurationDistribution;
  blocked_time: OperationWorkflowDurationDistribution;
  outcomes_truncated: boolean;
  outcomes: Array<OperationWorkflowRuleVersionOutcome>;
};

