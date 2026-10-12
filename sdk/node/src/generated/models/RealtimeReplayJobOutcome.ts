/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Realtime Replay Job Outcome.
 */
export type RealtimeReplayJobOutcome = {
  sequence: string;
  status: 'eligible' | 'replayed' | 'ordering_blocked' | 'stale' | 'not_dead' | 'missing' | 'deadline_expired' | 'invalid_deadline';
  /**
   * Per-item observation time; absent in older retained job outcomes.
   */
  checked_at?: string;
};

