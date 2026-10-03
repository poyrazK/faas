/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Immutable, versioned customer price shared across every app attributed to one platform tenant.
 */
export type PlatformTenantRateCardResponse = {
  id: string;
  tenant_id: string;
  currency: string;
  unit: 'request';
  price_millicents_per_unit: number;
  effective_from: string;
  created_at: string;
};

