/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APIConsumerUsageStatementBucketResponse } from './APIConsumerUsageStatementBucketResponse.js';
/**
 * Immutable, auditable API consumer usage snapshot.
 */
export type APIConsumerUsageStatementResponse = {
  id: string;
  consumer_id: string;
  period_start: string;
  period_end: string;
  status: 'draft' | 'finalized';
  currency?: string;
  billable_units: number;
  unpriced_units: number;
  amount_millicents: number;
  priced: boolean;
  buckets: Array<APIConsumerUsageStatementBucketResponse>;
  as_of: string;
  created_at: string;
  finalized_at?: string | null;
};

