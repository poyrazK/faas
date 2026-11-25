/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type ManagedPostgresBindingTimeouts = {
  binding_id: string;
  statement_timeout_ms: number;
  lock_timeout_ms: number;
  idle_in_transaction_session_timeout_ms: number;
  revision: number;
  /**
   * Default means no policy was saved. Pending means the defaults are saved and awaiting application. Refreshing means PostgreSQL accepted the settings and the pool's cache may still expire. Applied means the refresh window elapsed and new transactions use fresh backends.
   */
  state: 'default' | 'pending' | 'refreshing' | 'applied';
  applied_at?: string;
};

