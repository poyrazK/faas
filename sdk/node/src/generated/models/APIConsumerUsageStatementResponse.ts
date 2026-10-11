/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APIConsumerUsageStatementBucketResponse } from './APIConsumerUsageStatementBucketResponse.js';
/**
 * Immutable, auditable API consumer usage snapshot. One period can hold several revisions: a changed draft is superseded by the next revision, and every revision after a finalized one carries only usage that arrived later.
 */
export type APIConsumerUsageStatementResponse = {
  id: string;
  consumer_id: string;
  period_start: string;
  period_end: string;
  revision: number;
  status: 'draft' | 'finalized' | 'superseded';
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

