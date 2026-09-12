/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One UTC minute captured in a durable usage statement.
 */
export type APIConsumerUsageStatementBucketResponse = {
  window_start: string;
  billable_units: number;
  rate_card_id?: string;
  currency?: string;
  price_millicents_per_unit?: number;
  amount_millicents: number;
};

