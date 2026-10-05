/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type InvocationAttemptResponse = {
  id: number;
  invocation_id: string;
  replay_generation: number;
  attempt: number;
  started_at: string;
  finished_at?: string;
  /**
   * Dispatch evidence; unknown does not prove whether the handler ran.
   */
  outcome: 'running' | 'succeeded' | 'retry' | 'failed' | 'dead_letter' | 'cancelled' | 'unknown';
  error_detail?: string;
  next_attempt_at?: string;
  /**
   * Closed attempts can expire earlier if their invocation is deleted; running attempts are not pruned.
   */
  retain_until: string;
};

