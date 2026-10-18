/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Safe identity and closed diagnostics for a loaded rule or an invalid empty group. Counts are null unless healthy; age is null for absent or future evaluation clocks within the accepted skew tolerance.
 */
export type ObsRuleHealthItem = {
  kind: 'rule' | 'group';
  id: string;
  group_id: string;
  group_name?: string;
  file_name?: string;
  rule_name?: string;
  state: 'healthy' | 'failed' | 'stale' | 'invalid_clock' | 'unavailable' | 'invalid';
  issues: Array<'invalid_group_identity' | 'duplicate_group_identity' | 'duplicate_rule_identity' | 'unexpected_rule_type' | 'invalid_rule_definition' | 'evaluation_failed' | 'evaluation_unavailable' | 'missing_evaluation' | 'evaluation_clock_in_future' | 'evaluation_stale' | 'invalid_alert_observation' | 'inconsistent_rule_state'>;
  reported_state: 'inactive' | 'pending' | 'firing' | 'unknown';
  last_evaluated_at?: string;
  evaluation_age_seconds: number | null;
  has_evaluation_error: boolean;
  pending: number | null;
  firing: number | null;
  runbook_url?: string;
};

