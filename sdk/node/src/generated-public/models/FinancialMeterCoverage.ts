/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Completeness and freshness of authoritative retained meter evidence.
 */
export type FinancialMeterCoverage = {
  complete: boolean;
  fresh: boolean;
  expected_minutes: number;
  complete_minutes: number;
  unpriced_quantity: number;
  non_billable_quantity: number;
  reasons: Array<string>;
};

