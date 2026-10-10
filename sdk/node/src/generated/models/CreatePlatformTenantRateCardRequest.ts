/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APIConsumerRateCardTier } from './APIConsumerRateCardTier.js';
/**
 * Immutable tenant-wide price per attributed request unit. Currency defaults to EUR and effective_from defaults to the next UTC minute.
 */
export type CreatePlatformTenantRateCardRequest = {
  currency?: string;
  price_millicents_per_unit: number;
  /**
   * Free request units per tenant per UTC calendar month across every attributed app, consumed in minute order (ADR-975). Statements for periods it prices must cover one calendar month.
   */
  included_units_per_month?: number;
  /**
   * Optional graduated ladder counted per tenant per UTC calendar month across every attributed app (ADR-975). Replaces price_millicents_per_unit and included_units_per_month; the last step's up_to is null.
   */
  tiers?: Array<APIConsumerRateCardTier>;
  /**
   * UTC minute at which the version starts; omitted means the next UTC minute. Cannot be in the past once any tenant card has included units or tiers.
   */
  effective_from?: string | null;
};

