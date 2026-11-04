/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowDurationDistribution } from './OperationWorkflowDurationDistribution.js';
/**
 * One exact contract, decision or path, rule version and reported outcome identity. Each contributing workflow counts once. Durations cover the full completed workflow and use the existing performance history rules; they do not measure rule causation or decision execution time. Overlapping blockers count once in each workflow's blocked duration.
 */
export type OperationWorkflowDecisionOutcomeGroup = {
  kind: 'decision' | 'path';
  contract_version: number;
  decision_code?: string;
  rule_id?: string;
  rule_version?: string;
  group_id?: string;
  path_id?: string;
  outcome_code: string;
  completion_time: OperationWorkflowDurationDistribution;
  blocked_time: OperationWorkflowDurationDistribution;
};

