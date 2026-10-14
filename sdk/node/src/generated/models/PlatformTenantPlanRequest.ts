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
  /**
   * What the card a plan assignment creates prices (ADR-1310). Assigning a plan whose unit differs from the tenant's current card starts its card at the next UTC month boundary.
   */
  unit?: 'request' | 'compute_second' | 'response_megabyte';
  /**
   * How the plan's ladder prices a tenant's month (ADR-1309); volume needs tiers.
   */
  tier_mode?: 'graduated' | 'volume';
};

