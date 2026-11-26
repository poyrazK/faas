/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Successful request counts for one UTC hour with evidence of a gap.
 */
export type BillingRunUsageHoldHour = {
  hour: string;
  ledger_requests_at_hold: number;
  telemetry_requests_at_hold: number;
  /**
   * Highest successful request count captured from telemetry for this hour.
   */
  required_requests: number;
  ledger_requests: number;
};

