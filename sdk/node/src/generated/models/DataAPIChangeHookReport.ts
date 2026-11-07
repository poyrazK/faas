/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A runtime's change hook delivery report, read from its change event table. oldest_pending_at is present exactly when changes are pending.
 */
export type DataAPIChangeHookReport = {
  version: 1;
  pending: number;
  failed: number;
  oldest_pending_at?: string;
  last_published_at?: string;
  last_failure_code?: string;
  publishing: 'ok' | 'retrying';
};

