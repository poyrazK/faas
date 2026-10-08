/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type EventRecoveryCapacityWait = {
  scope: 'account' | 'app' | 'consumer' | 'unknown';
  gate: 'pending_delivery_limit' | 'unknown';
  /**
   * Safe explanation of the observed admission gate; does not include payloads or resolved work keys.
   */
  explanation: string;
  started_at: string;
  observed_at: string;
  age_seconds: number;
};

