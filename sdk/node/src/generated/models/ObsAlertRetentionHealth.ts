/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Independent retention receipt and exact expired-episode backlog at the snapshot cutoff.
 */
export type ObsAlertRetentionHealth = {
  state: 'never_run' | 'current' | 'failed' | 'stale' | 'unknown';
  reason?: string;
  as_of: string;
  cutoff: string;
  last_attempt_at?: string;
  last_success_at?: string;
  last_pruned_episodes: number | null;
  expired_episodes: number;
  oldest_expired_at?: string;
};

