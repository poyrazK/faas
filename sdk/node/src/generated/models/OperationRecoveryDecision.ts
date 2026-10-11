/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Immutable historical recovery acknowledgement; excludes raw evidence, result and credentials.
 */
export type OperationRecoveryDecision = {
  operation_id: string;
  recovery_id: string;
  request_fingerprint: string;
  expected_generation: number;
  generation: number;
  expected_inspection_revision?: string;
  resolution: 'succeeded' | 'failed' | 'cancelled' | 'safe_to_retry';
  state: 'accepted' | 'succeeded' | 'failed' | 'cancelled';
  invocation_id?: string;
  job_run_id?: string;
  workflow_run_id?: string;
  recorded_at: string;
  expires_at: string;
};

