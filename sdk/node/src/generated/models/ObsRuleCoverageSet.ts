/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Versioned minimum expectation set with a digest of its exact canonical definitions and safe source basenames.
 */
export type ObsRuleCoverageSet = {
  id: 'gregale-core-v1' | 'gregale-full-v1';
  revision: string;
  /**
   * Sorted safe source basenames selected by the expectation set.
   */
  files: Array<string>;
  expected_rules: number;
};

