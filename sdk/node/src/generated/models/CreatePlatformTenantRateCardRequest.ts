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
   * Free request units per tenant per UTC calendar month across every attributed app, consumed in minute order. Statements for periods it prices must cover one calendar month.
   */
  included_units_per_month?: number;
  /**
   * Optional graduated ladder counted per tenant per UTC calendar month across every attributed app. Replaces price_millicents_per_unit and included_units_per_month; the last step's up_to is null.
   */
  tiers?: Array<APIConsumerRateCardTier>;
  /**
   * Counts each request on a listed "METHOD /template" route as that many units (1..1000, at most 50 routes), including surface and JWT-rule requests. Unlisted routes count 1. Statements for periods it prices must cover one calendar month.
   */
  route_weights?: Record<string, number>;
  /**
   * UTC minute at which the version starts; omitted means the next UTC minute. Cannot be in the past once any tenant card has included units, tiers, route weights, monthly fees, or a unit other than requests.
   */
  effective_from?: string | null;
  /**
   * What the card prices: requests; the compute seconds or response megabytes of billable requests, rounded per subject minute; or app:<meter>, the units the app reports in its Gregale-Usage header. Cards of different units price side by side; non-request cards take no route weights, are month-priced and cannot be backdated.
   */
  unit?: string;
  /**
   * How the tenant ladder prices a month: graduated by each unit's position, or volume, every unit of the month at the step the tenant's total reaches. volume needs tiers.
   */
  tier_mode?: 'graduated' | 'volume';
  /**
   * Fixed charge per tenant per UTC month while this card is in force when the tenant's month starts. Statements for periods it prices must cover one calendar month.
   */
  base_fee_millicents_per_month?: number;
  /**
   * Minimum usage charge per tenant per UTC month for this card's unit; statements top up the difference.
   */
  minimum_millicents_per_month?: number;
  /**
   * Scale the base fee and minimum in the tenant's first month to the share of it left after the tenant was created.
   */
  prorate_monthly_fees?: boolean;
};

