/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Exact duration dimension and optional contract, state, blocker or owner selectors for contributor reads.
 */
export type OperationWorkflowPerformanceGroup = {
  condition_id?: string;
  /**
   * Exact public condition description in the returned group; UTF-8 byte bound without controls.
   */
  condition_description?: string;
  condition_result?: 'pass' | 'fail' | 'unknown';
  kind?: 'decision' | 'path';
  decision_code?: string;
  rule_id?: string;
  rule_version?: string;
  group_id?: string;
  path_id?: string;
  outcome_code?: string;
  dimension: 'state_time' | 'blocked_time' | 'verification_wait' | 'state' | 'blocker' | 'verification_owner' | 'decision_completion' | 'decision_blocked' | 'condition_completion' | 'condition_blocked';
  contract_version?: number;
  state?: string;
  operation?: string;
  code?: string;
  owner?: string;
  unassigned?: boolean;
};

