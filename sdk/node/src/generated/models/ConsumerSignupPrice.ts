/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One unit's current price on a plan.
 */
export type ConsumerSignupPrice = {
  unit: string;
  currency: string;
  price_millicents_per_unit: number;
  included_units_per_month?: number;
  tiered?: boolean;
  base_fee_millicents_per_month?: number;
  minimum_millicents_per_month?: number;
};

