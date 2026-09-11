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
   * UTC minute at which this version starts; omitted means the next UTC minute.
   */
  effective_from?: string | null;
};

