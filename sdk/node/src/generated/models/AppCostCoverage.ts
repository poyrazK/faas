/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Source sampling windows with per-app quantity coverage explicitly unknown.
 */
export type AppCostCoverage = {
  complete: boolean;
  fresh: boolean;
  expected_minutes: number;
  complete_minutes: number;
  /**
   * False; account-wide unpriced and nonbillable quantities are not exposed.
   */
  quantity_coverage_known: boolean;
  reasons: Array<string>;
};

