/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Recorded delivery outcome for a request to cancel a pooled query.
 */
export type ManagedPostgresQueryCancellation = {
  database_id: string;
  target_id: string;
  outcome: 'signal_sent' | 'stale_target' | 'unconfirmed';
};

