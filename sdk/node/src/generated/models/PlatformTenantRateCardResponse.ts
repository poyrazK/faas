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
  /**
   * What the tenant card prices.
   */
  unit: string;
  /**
   * The card's fixed monthly charge per tenant.
   */
  base_fee_millicents_per_month?: number;
  /**
   * The card's minimum monthly usage charge per tenant.
   */
  minimum_millicents_per_month?: number;
  /**
   * Whether the first month's fees are prorated.
   */
  prorate_monthly_fees?: boolean;
  price_millicents_per_unit: number;
  /**
   * Free units per tenant per UTC calendar month.
   */
  included_units_per_month: number;
  /**
   * Ladder counted per tenant per UTC calendar month; absent for flat cards.
   */
  tiers?: Array<APIConsumerRateCardTier>;
  /**
   * How the tenant ladder prices a month.
   */
  tier_mode?: 'graduated' | 'volume';
  /**
   * Units per request on a route; absent when every request counts 1.
   */
  route_weights?: Record<string, number>;
  effective_from: string;
  created_at: string;
};

