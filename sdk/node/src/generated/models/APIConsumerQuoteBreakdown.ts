/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Units and charge allocated to a route or meter at one rate.
 */
export type APIConsumerQuoteBreakdown = {
  /**
   * Method and route template for attributed request units.
   */
  route?: string;
  unit: string;
  model?: string;
  rate_card_id?: string;
  price_millicents_per_unit: number;
  billable_units: number;
  /**
   * Units covered by the flat card's monthly allowance.
   */
  included_units: number;
  /**
   * Reported model units waived in this breakdown.
   */
  waived_units?: number;
  charged_units: number;
  amount_millicents: number;
  trial?: boolean;
};

