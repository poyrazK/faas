/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Operator attestation after database inspection and cleanup execution quiescence, bound to a different verified active replacement. This request records recovery and retires drained old code; it does not execute SQL or replay cleanup.
 */
export type ProjectMigrationRecoveryRequest = {
  request_id: string;
  expected_task_id: string;
  expected_generation: number;
  replacement_attempt_id: string;
  /**
   * Operator must attest that database state was inspected and repaired.
   */
  database_state_checked: boolean;
  /**
   * Operator must confirm the original cleanup execution has stopped.
   */
  execution_quiescent: boolean;
  evidence_hash: string;
};

