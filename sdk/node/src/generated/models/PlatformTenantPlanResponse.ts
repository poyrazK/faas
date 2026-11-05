/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APIConsumerRateCardTier } from './APIConsumerRateCardTier.js';
import type { PlatformTenantPlanBudget } from './PlatformTenantPlanBudget.js';
/**
 * One tenant plan.
 */
export type PlatformTenantPlanResponse = {
  id: string;
  name: string;
  currency: string;
  price_millicents_per_unit: number;
  included_units_per_month: number;
  tiers?: Array<APIConsumerRateCardTier>;
  route_weights?: Record<string, number>;
  request_budget?: PlatformTenantPlanBudget;
  /**
   * What the plan's card prices.
   */
  unit?: string;
  base_fee_millicents_per_month?: number;
  minimum_millicents_per_month?: number;
  prorate_monthly_fees?: boolean;
  /**
   * How the plan's ladder prices a month.
   */
  tier_mode?: 'graduated' | 'volume';
  created_at: string;
  updated_at: string;
};

