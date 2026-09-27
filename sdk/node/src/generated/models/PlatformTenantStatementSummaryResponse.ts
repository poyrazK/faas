/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Finalized statement metadata without the potentially large line-item collection; retrieve line items by statement ID.
 */
export type PlatformTenantStatementSummaryResponse = {
  id: string;
  tenant_id: string;
  period_start: string;
  period_end: string;
  revision: number;
  status: 'finalized';
  currency?: string;
  billable_units: number;
  unpriced_units: number;
  amount_millicents: number;
  as_of: string;
  created_at: string;
  finalized_at?: string;
};

