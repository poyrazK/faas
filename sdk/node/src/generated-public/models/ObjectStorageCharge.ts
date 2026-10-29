/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Current UTC-month customer charge estimate. This is not an invoice until a billing provider posts a line item.
 */
export type ObjectStorageCharge = {
  currency: string;
  storage_millicents: number;
  requests_millicents: number;
  egress_millicents: number;
  total_millicents: number;
};

