/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One prepaid credit grant to a platform tenant.
 */
export type PlatformTenantCreditResponse = {
  id: string;
  tenant_id: string;
  currency: string;
  amount_millicents: number;
  remaining_millicents: number;
  status: 'active' | 'used' | 'expired' | 'voided';
  note?: string;
  expires_at?: string;
  voided_at?: string;
  created_at: string;
};

