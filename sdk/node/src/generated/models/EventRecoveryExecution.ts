/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Read-only observation of this job's admitted replay generation. Later replays are not attributed to this item. Missing history, untracked legacy items, or uncertain outcomes report unknown. Omitted for routing recovery and items not admitted.
 */
export type EventRecoveryExecution = {
  observed_at: string;
  state: 'queued' | 'running' | 'retrying' | 'succeeded' | 'failed' | 'dead_lettered' | 'expired' | 'cancelled' | 'superseded' | 'unknown';
  source: 'invocation' | 'attempt_history' | 'unavailable';
  /**
   * Recorded dispatch attempt count for the tracked generation.
   */
  attempts: number;
  /**
   * Recorded terminal completion time when available.
   */
  completed_at?: string;
};

