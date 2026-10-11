/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Current retained invocation state. The recipient execution is the original deterministic invocation; recovery and history contain trusted generic replay invocations.
 */
export type EventReceiptExecutionResponse = {
  invocation_id: string;
  /**
   * Trusted immediate parent for a generic replay; its execution record may have expired.
   */
  replayed_from_invocation_id?: string;
  state: 'pending' | 'dispatching' | 'completed' | 'failed' | 'cancelled' | 'superseded' | 'expired' | 'dead_letter';
  attempts: number;
  /**
   * This invocation's budget generation, updated by in-place dead-letter replay.
   */
  replay_generation: number;
  /**
   * Next pending invocation due time; dispatch may be delayed by admission.
   */
  next_attempt_at?: string;
  created_at: string;
  completed_at?: string;
  last_error?: string;
};

