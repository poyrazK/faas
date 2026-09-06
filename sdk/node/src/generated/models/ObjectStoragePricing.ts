/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Operator-supplied customer rate card. Rates are integer millicents and are not provider-specific.
 */
export type ObjectStoragePricing = {
  currency: string;
  storage_millicents_per_gib_month: number;
  requests_millicents_per_million: number;
  egress_millicents_per_gib: number;
};

