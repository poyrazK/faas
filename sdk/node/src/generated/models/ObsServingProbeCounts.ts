/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Counts cover every expected pair. Reporting is known true only for evaluated pairs with a result; unknown evaluations never count as healthy or reporting.
 */
export type ObsServingProbeCounts = {
  expected: number;
  reporting: number;
  healthy: number;
  missing: number;
  stale: number;
  invalid: number;
  content_disabled: number;
  budget_mismatch: number;
  failing: number;
  unknown: number;
};

