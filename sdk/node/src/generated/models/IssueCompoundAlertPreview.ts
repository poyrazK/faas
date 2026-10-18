/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Predicate results and retained shared-window counts from a read-only compound-rule evaluation.
 */
export type IssueCompoundAlertPreview = {
  issue_id: string;
  rule_key: string;
  environment: string;
  policy_revision: number;
  window_start: string;
  window_end: string;
  observed_events: number;
  identified_customers: number;
  enabled: boolean;
  environment_matches: boolean;
  events_matched: boolean;
  customers_matched: boolean;
  suppressed: boolean;
  cooldown_ready: boolean;
  allowed: boolean;
  reason: 'disabled' | 'environment_excluded' | 'suppressed' | 'event_threshold' | 'customer_threshold' | 'cooldown' | 'eligible';
  next_eligible_at?: string;
};

