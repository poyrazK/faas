/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Version-checked manual retry with an optional new delivery time.
 */
export type ManagedRealtimeScheduleRetryRequest = {
  expected_version: number;
  /**
   * Optional future retry time within 30 days; omit to retry on the next worker pass.
   */
  deliver_at?: string;
};

