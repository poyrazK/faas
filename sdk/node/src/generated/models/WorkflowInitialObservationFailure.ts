/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Last deterministic manual observation failure. Retry uses bounded backoff; a successful observation clears this metadata. Codes never include database error messages.
 */
export type WorkflowInitialObservationFailure = {
  code: 'invalid_recovery_identity' | 'invalid_observation_data';
  attempts: number;
  failed_at: string;
  retry_at: string;
};

