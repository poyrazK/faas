/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Read-only observation of this job's exact admitted replay generation. Saved confirmed terminal results take precedence and survive execution-history pruning until recovery job retention. Later replays never replace them. Missing evidence, untracked legacy items, or uncertain outcomes report unknown. Omitted for routing recovery and items not admitted.
 */
export type EventRecoveryExecution = {
  /**
   * When terminal evidence was saved; present only for recovery_result.
   */
  recorded_at?: string;
  /**
   * Original source of a saved terminal result; present only for recovery_result.
   */
  evidence_source?: 'invocation' | 'attempt_history';
  observed_at: string;
  state: 'queued' | 'running' | 'retrying' | 'succeeded' | 'failed' | 'dead_lettered' | 'expired' | 'cancelled' | 'superseded' | 'unknown';
  source: 'invocation' | 'attempt_history' | 'recovery_result' | 'unavailable';
  /**
   * Recorded dispatch attempt count for the tracked generation.
   */
  attempts: number;
  /**
   * Recorded terminal completion time when available.
   */
  completed_at?: string;
};

