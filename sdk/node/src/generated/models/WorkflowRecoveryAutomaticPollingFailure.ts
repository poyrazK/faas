/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Last deterministic automatic polling error. A committed deferral rolls back execution writes and retries with bounded backoff. Successful polling or policy update clears it; raw database messages are never stored.
 */
export type WorkflowRecoveryAutomaticPollingFailure = {
  code: 'invalid_policy_authority' | 'invalid_polling_data' | 'invalid_recovery_identity';
  attempts: number;
  failed_at: string;
  retry_at: string;
};

