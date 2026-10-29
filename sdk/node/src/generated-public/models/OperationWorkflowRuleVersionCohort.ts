/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowDurationDistribution } from './OperationWorkflowDurationDistribution.js';
import type { OperationWorkflowRuleVersionOutcome } from './OperationWorkflowRuleVersionOutcome.js';
/**
 * Complete completed workflows with a latest reported outcome and exactly one observed version for the same contract/decision/rule identity before the first terminal report. Detection occurs before version truncation. Incomplete histories never qualify. Statistics exclude overlapping workflows and are still observational rather than causal.
 */
export type OperationWorkflowRuleVersionCohort = {
  eligible_workflow_count: number;
  completion_time: OperationWorkflowDurationDistribution;
  blocked_time: OperationWorkflowDurationDistribution;
  outcomes_truncated: boolean;
  outcomes: Array<OperationWorkflowRuleVersionOutcome>;
};

