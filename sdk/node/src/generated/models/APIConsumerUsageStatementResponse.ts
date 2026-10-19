/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APIConsumerStatementFeeResponse } from './APIConsumerStatementFeeResponse.js';
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
  /**
   * The month's base fees and minimum top-ups (ADR-1316); amount_millicents includes them.
   */
  fees?: Array<APIConsumerStatementFeeResponse>;
  as_of: string;
  created_at: string;
  finalized_at?: string | null;
  /**
   * Prepaid credit drawn when the statement was finalized (ADR-1306).
   */
  credit_applied_millicents?: number;
  /**
   * amount_millicents less credit_applied_millicents: what is left to bill.
   */
  amount_due_millicents?: number;
};

