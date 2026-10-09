/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One durable usage minute priced by the rate card effective at that minute.
 */
export type APIConsumerUsageQuoteBucketResponse = {
  window_start: string;
  billable_units: number;
  rate_card_id?: string;
  currency?: string;
  price_millicents_per_unit?: number;
  /**
   * Units billed at the price; the rest are covered by the rate card's monthly allowance.
   */
  charged_units: number;
  amount_millicents: number;
};

