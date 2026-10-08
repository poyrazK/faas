/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type EventRecoveryRateRequest = {
  /**
   * Optional operator reason, limited to 512 UTF-8 bytes without control characters. Stored only in audit history; omitted from frozen selection. Preview does not record it.
   */
  reason?: string;
  /**
   * Maximum items per second. Does not reset spent permits or existing admission waits.
   */
  rate_per_second: number;
};

