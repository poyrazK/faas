/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APIConsumerRateCardTier } from './APIConsumerRateCardTier.js';
/**
 * Immutable, versioned customer price shared across every app attributed to one platform tenant.
 */
export type PlatformTenantRateCardResponse = {
  id: string;
  tenant_id: string;
  currency: string;
  unit: 'request';
  price_millicents_per_unit: number;
  /**
   * Free units per tenant per UTC calendar month (ADR-939).
   */
  included_units_per_month: number;
  /**
   * Graduated ladder counted per tenant per UTC calendar month (ADR-939); absent for flat cards.
   */
  tiers?: Array<APIConsumerRateCardTier>;
  effective_from: string;
  created_at: string;
};

