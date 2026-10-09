/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Persisted step status and action kind without customer values.
 */
export type WorkflowDiagnosticStep = {
  step_name: string;
  status: 'pending' | 'running' | 'awaiting_event' | 'succeeded' | 'failed' | 'dead' | 'skipped';
  kind: 'action' | 'for_each' | 'join' | 'timer' | 'event' | 'callback' | 'condition' | 'unknown';
  attempt: number;
  retry_base: number;
  next_retry_at?: string;
  next_check_at?: string;
};

