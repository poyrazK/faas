/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APIConsumerRateCardTier } from './APIConsumerRateCardTier.js';
/**
 * Immutable, versioned app-level price for one API request unit.
 */
export type APIConsumerRateCardResponse = {
  id: string;
  app_id: string;
  currency: string;
  unit: 'request';
  price_millicents_per_unit: number;
  included_units_per_month: number;
  tiers?: Array<APIConsumerRateCardTier>;
  /**
   * Units charged per request on a listed route; unlisted routes count 1.
   */
  route_weights?: Record<string, number>;
  plan_id?: string;
  effective_from: string;
  created_at: string;
};

