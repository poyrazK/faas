/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One current native policy observation with a stable reason code.
 */
export type AppOperationCheck = {
  status: 'allowed' | 'blocked' | 'unknown';
  /**
   * Stable native policy reason code; no customer-provided text.
   */
  reason: string;
};

