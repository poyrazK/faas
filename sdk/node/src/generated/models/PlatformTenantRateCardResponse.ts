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
   * What the tenant card prices (ADR-1310, ADR-1319).
   */
  unit: string;
  /**
   * The card's fixed monthly charge per tenant (ADR-1322).
   */
  base_fee_millicents_per_month?: number;
  /**
   * The card's minimum monthly usage charge per tenant (ADR-1322).
   */
  minimum_millicents_per_month?: number;
  /**
   * Whether the first month's fees are prorated (ADR-1325).
   */
  prorate_monthly_fees?: boolean;
  price_millicents_per_unit: number;
  /**
   * Free units per tenant per UTC calendar month (ADR-975).
   */
  included_units_per_month: number;
  /**
   * Ladder counted per tenant per UTC calendar month (ADR-975); absent for flat cards.
   */
  tiers?: Array<APIConsumerRateCardTier>;
  /**
   * How the tenant ladder prices a month (ADR-1309).
   */
  tier_mode?: 'graduated' | 'volume';
  /**
   * Units per request on a route (ADR-1307, ADR-1310); absent when every request counts 1.
   */
  route_weights?: Record<string, number>;
  effective_from: string;
  created_at: string;
};

