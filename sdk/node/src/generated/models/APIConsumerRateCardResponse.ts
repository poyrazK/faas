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
  /**
   * What the card prices (ADR-1308, ADR-1318): billable requests, their compute seconds or response megabytes, or the units of an app meter.
   */
  unit: string;
  /**
   * The card's fixed monthly charge per consumer (ADR-1316).
   */
  base_fee_millicents_per_month?: number;
  /**
   * The card's minimum monthly usage charge per consumer (ADR-1316).
   */
  minimum_millicents_per_month?: number;
  /**
   * Whether a consumer's first-month fees are prorated (ADR-1325).
   */
  prorate_monthly_fees?: boolean;
  price_millicents_per_unit: number;
  included_units_per_month: number;
  tiers?: Array<APIConsumerRateCardTier>;
  /**
   * How the ladder prices a consumer's month (ADR-1309).
   */
  tier_mode?: 'graduated' | 'volume';
  /**
   * Units charged per request on a listed route; unlisted routes count 1.
   */
  route_weights?: Record<string, number>;
  plan_id?: string;
  effective_from: string;
  created_at: string;
};

