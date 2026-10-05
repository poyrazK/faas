/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Immutable version of a meter's exact price and allowance terms.
 */
export type FinancialPrice = {
  version: string;
  meter: string;
  currency: 'EUR';
  unit: string;
  unit_quantity: number;
  millicents_per_unit: number;
  included_quantity: number;
};

