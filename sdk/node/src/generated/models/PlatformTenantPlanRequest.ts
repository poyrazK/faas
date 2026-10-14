/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APIConsumerRateCardTier } from './APIConsumerRateCardTier.js';
import type { PlatformTenantPlanBudget } from './PlatformTenantPlanBudget.js';
/**
 * A reusable tenant plan (ADR-1307): rate card terms and an optional request budget that assigning the plan applies.
 */
export type PlatformTenantPlanRequest = {
  name: string;
  currency: string;
  price_millicents_per_unit: number;
  included_units_per_month?: number;
  tiers?: Array<APIConsumerRateCardTier>;
  route_weights?: Record<string, number>;
  request_budget?: PlatformTenantPlanBudget;
};

