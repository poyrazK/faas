/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A tenant's spend cap and its last check (ADR-20261019100331).
 */
export type PlatformTenantSpendCapResponse = {
  tenant_id: string;
  currency: string;
  monthly_cap_millicents: number;
  /**
   * True while the gateway refuses the tenant this month.
   */
  capped: boolean;
  /**
   * UTC month of the last check.
   */
  month?: string;
  /**
   * The month's usage after prepaid credit at the last check; monthly fees are not counted.
   */
  amount_due_millicents?: number;
  checked_at?: string;
  created_at: string;
  updated_at: string;
};

