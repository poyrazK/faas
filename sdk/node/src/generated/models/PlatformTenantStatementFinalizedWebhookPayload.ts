/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APIConsumerStatementFeeResponse } from './APIConsumerStatementFeeResponse.js';
import type { PlatformTenantStatementLineResponse } from './PlatformTenantStatementLineResponse.js';
/**
 * Immutable cross-app statement snapshot delivered once per finalized revision to tenant-scoped receivers.
 */
export type PlatformTenantStatementFinalizedWebhookPayload = {
  platform_tenant_id: string;
  /**
   * Platform owner's stable customer reference.
   */
  external_ref: string;
  statement_id: string;
  revision: number;
  status: 'finalized';
  period_start: string;
  period_end: string;
  currency: string;
  billable_units: number;
  unpriced_units: number;
  amount_millicents: number;
  priced: boolean;
  lines: Array<PlatformTenantStatementLineResponse>;
  /**
   * The revision's monthly base fees and minimum top-ups, included in amount_millicents.
   */
  fees?: Array<APIConsumerStatementFeeResponse>;
  /**
   * Tenant prepaid credit this finalized revision drew.
   */
  credit_applied_millicents?: number;
  /**
   * What remains to invoice after the credit.
   */
  amount_due_millicents?: number;
  as_of: string;
  finalized_at: string;
};

