/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Timeout settings to change on a ready pooled application binding.
 */
export type SetManagedPostgresBindingTimeoutsRequest = {
  statement_timeout_ms?: number;
  lock_timeout_ms?: number;
  idle_in_transaction_session_timeout_ms?: number;
};

