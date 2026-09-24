/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PlatformTenantStatementLineResponse } from './PlatformTenantStatementLineResponse.js';
/**
 * One immutable initial or additive adjustment revision for a cross-app customer period.
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
  as_of: string;
  created_at: string;
  finalized_at?: string;
};

