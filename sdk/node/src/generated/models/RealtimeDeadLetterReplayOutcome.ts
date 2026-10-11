/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type RealtimeDeadLetterReplayOutcome = {
  sequence: string;
  status: 'replayed' | 'ordering_blocked' | 'stale' | 'not_dead' | 'missing' | 'deadline_expired' | 'invalid_deadline';
};

