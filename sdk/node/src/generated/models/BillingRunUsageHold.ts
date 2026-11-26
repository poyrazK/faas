/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { BillingRunItemResponse } from './BillingRunItemResponse.js';
import type { BillingRunUsageHoldHour } from './BillingRunUsageHoldHour.js';
/**
 * Billing run usage hold evidence and its current ledger comparison.
 */
export type BillingRunUsageHold = {
  item: BillingRunItemResponse;
  /**
   * When the run recorded the original gap.
   */
  held_at?: string;
  /**
   * Minimum successful requests still missing from the ledger across recorded hours.
   */
  missing_requests: number;
  hours: Array<BillingRunUsageHoldHour>;
};

