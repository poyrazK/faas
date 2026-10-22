/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Realtime Dead Letter Replay Outcome.
 */
export type RealtimeDeadLetterReplayOutcome = {
  sequence: string;
  status: 'replayed' | 'ordering_blocked' | 'stale' | 'not_dead' | 'missing' | 'deadline_expired' | 'invalid_deadline';
};

