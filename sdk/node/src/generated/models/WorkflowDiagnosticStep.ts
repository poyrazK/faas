/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Persisted step status and action kind without customer values.
 */
export type WorkflowDiagnosticStep = {
  /**
   * Advisory manual HTTP step retry eligibility using the captured graph and current quota. Absent means unavailable or ineligible. Actual retry rechecks state and quota.
   */
  retry_eligible?: boolean;
  /**
   * Skipped dependency descendants that would reopen with this manual retry. Omitted when empty or ineligible.
   */
  retry_reopened_steps?: Array<string>;
  step_name: string;
  status: 'pending' | 'running' | 'awaiting_event' | 'succeeded' | 'failed' | 'dead' | 'skipped';
  kind: 'action' | 'for_each' | 'join' | 'timer' | 'event' | 'callback' | 'condition' | 'unknown';
  attempt: number;
  retry_base: number;
  next_retry_at?: string;
  next_check_at?: string;
};

