/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APIConsumerRateCardTier } from './APIConsumerRateCardTier.js';
/**
 * Immutable app-level request price. Currency defaults to EUR and effective_from defaults to the next UTC minute.
 */
export type CreateAPIConsumerRateCardRequest = {
  currency?: string;
  price_millicents_per_unit: number;
  /**
   * Free request units per consumer per UTC calendar month while this card is effective, consumed in minute order. Once any card includes units, effective_from cannot be in the past.
   */
  included_units_per_month?: number;
  /**
   * Optional graduated ladder that replaces price_millicents_per_unit and included_units_per_month. Each consumer's units are counted per UTC calendar month in minute order and priced by the step their position falls in. Bounds increase strictly, only the last step is unbounded, and only the first step may be free. Statements of periods priced by a tiered card must cover exactly one UTC calendar month.
   */
  tiers?: Array<APIConsumerRateCardTier>;
  /**
   * Counts each request on a listed "METHOD /template" route as that many units (1..1000, at most 50 routes); unlisted routes count 1. Weighted units feed included units, tiers, and statements. Route labels match the app's declared or discovered route templates.
   */
  route_weights?: Record<string, number>;
  /**
   * UTC minute at which this version starts; omitted means the next UTC minute.
   */
  effective_from?: string | null;
};

