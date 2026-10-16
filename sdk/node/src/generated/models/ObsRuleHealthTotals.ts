/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Complete bounded source counts. Rule health categories partition rules; invalid groups include empty groups with missing or duplicate identity.
 */
export type ObsRuleHealthTotals = {
  groups: number;
  rules: number;
  healthy: number;
  failed: number;
  stale: number;
  invalid_clock: number;
  unavailable: number;
  invalid: number;
  invalid_groups: number;
};

