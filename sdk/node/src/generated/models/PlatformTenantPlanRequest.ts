/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APIConsumerRateCardTier } from './APIConsumerRateCardTier.js';
import type { PlatformTenantPlanBudget } from './PlatformTenantPlanBudget.js';
/**
 * A reusable tenant plan: rate card terms and an optional request budget that assigning the plan applies.
 */
export type PlatformTenantPlanRequest = {
  name: string;
  currency: string;
  price_millicents_per_unit: number;
  included_units_per_month?: number;
  tiers?: Array<APIConsumerRateCardTier>;
  route_weights?: Record<string, number>;
  request_budget?: PlatformTenantPlanBudget;
  /**
   * What the card a plan assignment creates prices. Assigning a plan also starts a free card for each other unit the tenant is charged for, so the plan is the tenant's whole price.
   */
  unit?: string;
  /**
   * Fixed monthly charge per tenant the plan's card sets.
   */
  base_fee_millicents_per_month?: number;
  /**
   * Minimum monthly usage charge per tenant the plan's card sets.
   */
  minimum_millicents_per_month?: number;
  /**
   * Prorate the first month's fees.
   */
  prorate_monthly_fees?: boolean;
  /**
   * How the plan's ladder prices a tenant's month; volume needs tiers.
   */
  tier_mode?: 'graduated' | 'volume';
};

