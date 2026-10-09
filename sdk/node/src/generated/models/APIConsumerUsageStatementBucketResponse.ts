/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One UTC minute captured in a durable usage statement.
 */
export type APIConsumerUsageStatementBucketResponse = {
  window_start: string;
  billable_units: number;
  rate_card_id?: string;
  currency?: string;
  price_millicents_per_unit?: number;
  /**
   * Units this revision bills at the price; the rest are covered by the monthly allowance. An adjustment may charge units it does not add when late usage exhausted the allowance sooner.
   */
  charged_units: number;
  /**
   * Units per step of a tiered rate card's ladder. In an adjustment revision, entries are differences and can be negative when late usage moved billed units into a cheaper step.
   */
  tier_units?: Array<number>;
  /**
   * Exact charge for this minute. Negative only on a tiered adjustment line that re-rates billed units into a cheaper step; a revision's total is never negative.
   */
  amount_millicents: number;
};

