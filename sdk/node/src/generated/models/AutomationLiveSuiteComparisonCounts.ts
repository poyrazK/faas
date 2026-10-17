/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Disjoint case classifications; all categories sum to total. Zero regressions alone is not evidence that the candidate passed.
 */
export type AutomationLiveSuiteComparisonCounts = {
  total: number;
  regressions: number;
  recoveries: number;
  unchanged: number;
  added: number;
  removed: number;
  incompatible: number;
  unknown: number;
};

