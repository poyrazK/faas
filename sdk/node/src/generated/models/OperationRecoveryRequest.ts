/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Explicit recovery decision with retained evidence and idempotent recovery identity.
 */
export type OperationRecoveryRequest = {
  recovery_id: string;
  expected_generation: number;
  resolution: 'succeeded' | 'failed' | 'cancelled' | 'safe_to_retry';
  evidence: string;
  /**
   * Required typed output for succeeded.
   */
  result?: any;
};

