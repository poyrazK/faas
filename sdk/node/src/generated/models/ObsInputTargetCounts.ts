/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Original scrape freshness partition; missing minimum is separate from observed target failures.
 */
export type ObsInputTargetCounts = {
  observed: number;
  fresh_up: number;
  down: number;
  stale: number;
  /**
   * Expected minimum less observed count, clamped at zero; null if the expectation is unknown.
   */
  missing_minimum: (number | null);
  /**
   * Observed count above fresh discovery count; null for static minimums or unknown expectations.
   */
  extra: (number | null);
};

