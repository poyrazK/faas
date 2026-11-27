/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Multiple attempts require safe_to_repeat=true. Only restore_failed, execution_transport_failed and lease_expired are retried. Each attempt has a separate Run and budget reservation; external side effects may repeat.
 */
export type ExecutionRetryPolicy = {
  max_attempts: number;
  backoff_ms?: number;
  safe_to_repeat?: boolean;
};

