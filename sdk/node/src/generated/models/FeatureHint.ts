/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One Gregale behavior or feature that applies to the scanned source: what the plan will do, how to change it, and the exact CLI commands. IDs are stable.
 */
export type FeatureHint = {
  /**
   * Stable hint ID, such as health-default-path or public-needs-api-key
   */
  id: string;
  /**
   * Workloads the hint is about
   */
  workloads?: Array<string>;
  /**
   * What Gregale will do with this source
   */
  applied: string;
  /**
   * How to change it
   */
  change?: string;
  /**
   * Exact gregale commands; <placeholders> need a value
   */
  commands?: Array<string>;
};

