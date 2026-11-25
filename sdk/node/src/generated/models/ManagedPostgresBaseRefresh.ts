/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type ManagedPostgresBaseRefresh = {
  started_at: string;
  finished_at?: string;
  outcome: 'running' | 'succeeded' | 'still_stale' | 'interrupted' | 'window_closed' | 'runtime_failed';
  /**
   * Rolling refresh phase when applicable.
   */
  phase?: string;
  /**
   * Reason the refresh is waiting or did not succeed.
   */
  reason?: string;
};

