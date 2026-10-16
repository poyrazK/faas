/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Full counts before preview truncation. Expected categories partition expected_rules; additional valid identities are allowed by the minimum set.
 */
export type ObsRuleCoverageTotals = {
  expected_rules: number;
  observed_rules: number;
  additional_rules: number;
  invalid_observed_rules: number;
  invalid_observed_groups: number;
  matching: number;
  missing: number;
  changed: number;
  ambiguous: number;
  unknown: number;
};

