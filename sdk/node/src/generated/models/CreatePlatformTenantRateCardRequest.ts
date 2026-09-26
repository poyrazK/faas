/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Immutable tenant-wide price per attributed request unit. Currency defaults to EUR and effective_from defaults to the next UTC minute.
 */
export type CreatePlatformTenantRateCardRequest = {
  currency?: string;
  price_millicents_per_unit: number;
  /**
   * UTC minute at which the version starts; omitted means the next UTC minute.
   */
  effective_from?: string | null;
};

