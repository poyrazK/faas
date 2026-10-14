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
   * Counts each request on a listed "METHOD /template" route as that many units (1..1000, at most 50 routes; ADR-1307), including surface and JWT-rule requests (ADR-1310). Unlisted routes count 1. Statements for periods it prices must cover one calendar month.
   */
  route_weights?: Record<string, number>;
  /**
   * UTC minute at which the version starts; omitted means the next UTC minute. Cannot be in the past once any tenant card has included units, tiers, route weights, or a compute or response-size unit.
   */
  effective_from?: string | null;
  /**
   * What the card prices (ADR-1310): requests, or the compute seconds or response megabytes of billable requests, rounded per subject minute. Compute and response-size cards take no route weights, are month-priced, cannot be backdated, and a change of unit starts on the 1st of a month at 00:00 UTC.
   */
  unit?: 'request' | 'compute_second' | 'response_megabyte';
  /**
   * How the tenant ladder prices a month (ADR-1309): graduated by each unit's position, or volume, every unit of the month at the step the tenant's total reaches. volume needs tiers.
   */
  tier_mode?: 'graduated' | 'volume';
};

