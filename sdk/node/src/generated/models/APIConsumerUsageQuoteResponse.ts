/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APIConsumerUsageQuoteBucketResponse } from './APIConsumerUsageQuoteBucketResponse.js';
/**
 * Deterministic estimate from durable API consumer usage and versioned app pricing; not an invoice.
 */
export type APIConsumerUsageQuoteResponse = {
  consumer_id: string;
  period_start: string;
  period_end: string;
  currency?: string;
  billable_units: number;
  unpriced_units: number;
  amount_millicents: number;
  priced: boolean;
  buckets: Array<APIConsumerUsageQuoteBucketResponse>;
  as_of: string;
};

