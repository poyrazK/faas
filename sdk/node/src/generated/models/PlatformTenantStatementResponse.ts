/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APIConsumerStatementFeeResponse } from './APIConsumerStatementFeeResponse.js';
import type { PlatformTenantStatementLineResponse } from './PlatformTenantStatementLineResponse.js';
/**
 * One immutable initial or additive adjustment revision for a cross-app customer period, with compact invoice lines and private minute-level adjustment coverage.
 */
export type PlatformTenantStatementResponse = {
  id: string;
  tenant_id: string;
  period_start: string;
  period_end: string;
  revision: number;
  status: 'draft' | 'finalized' | 'superseded';
  currency?: string;
  billable_units: number;
  unpriced_units: number;
  amount_millicents: number;
  lines: Array<PlatformTenantStatementLineResponse>;
  /**
   * Monthly base fees and minimum top-ups this revision bills; amount_millicents includes them. A revision can bill fees without lines for a tenant without usage.
   */
  fees?: Array<APIConsumerStatementFeeResponse>;
  /**
   * Prepaid credit drawn when the revision was finalized.
   */
  credit_applied_millicents?: number;
  /**
   * amount_millicents less credit_applied_millicents: what invoice exports bill.
   */
  amount_due_millicents?: number;
  as_of: string;
  created_at: string;
  finalized_at?: string;
};

