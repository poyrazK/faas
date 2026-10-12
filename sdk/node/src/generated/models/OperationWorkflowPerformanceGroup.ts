/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Exact duration dimension and optional contract, state, blocker or owner selectors for contributor reads.
 */
export type OperationWorkflowPerformanceGroup = {
  gap_status?: 'report_lost' | 'new_expectation' | 'still_missing' | 'expectation_changed' | 'contract_changed';
  /**
   * Optional active-condition contributor filter; other dimensions reject this selector.
   */
  condition_evidence_status?: 'within_budget' | 'overdue' | 'no_budget';
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
  dimension: 'state_time' | 'blocked_time' | 'verification_wait' | 'state' | 'blocker' | 'verification_owner' | 'decision_completion' | 'decision_blocked' | 'condition_completion' | 'condition_blocked' | 'active_condition_elapsed' | 'active_condition_blocked' | 'condition_refresh_owner' | 'condition_refresh_age' | 'condition_refresh_blocked' | 'condition_missing_owner' | 'condition_gap_owner' | 'condition_gap_queue';
  contract_version?: number;
  state?: string;
  operation?: string;
  code?: string;
  owner?: string;
  unassigned?: boolean;
};

