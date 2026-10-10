/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One step of a graduated per-request price ladder.
 */
export type APIConsumerRateCardTier = {
  /**
   * Exclusive upper bound of this step's monthly position; null for the unbounded last step.
   */
  up_to: number | null;
  price_millicents_per_unit: number;
};

