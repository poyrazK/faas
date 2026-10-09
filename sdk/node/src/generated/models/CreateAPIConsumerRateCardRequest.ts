/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
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
   * UTC minute at which this version starts; omitted means the next UTC minute.
   */
  effective_from?: string | null;
};

