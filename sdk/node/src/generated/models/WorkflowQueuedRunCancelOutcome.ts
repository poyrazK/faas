/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Eligibility or result for cancelling one queued workflow run.
 */
export type WorkflowQueuedRunCancelOutcome = {
  run_id: string;
  workflow_name?: string;
  /**
   * Preview classification or the final result after the atomic cancellation recheck.
   */
  outcome: 'eligible' | 'cancelled' | 'already_cancelled' | 'not_found' | 'workflow_mismatch' | 'not_queued' | 'already_started';
  status?: 'pending' | 'running' | 'awaiting_event' | 'succeeded' | 'failed' | 'dead';
  started_at?: string;
  scheduled_for?: string;
  created_at?: string;
  cancelled_at?: string;
};

