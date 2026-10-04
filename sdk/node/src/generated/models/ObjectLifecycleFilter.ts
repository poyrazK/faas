/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Empty filter selects all keys. Prefix and tags are conjunctive; at most ten tags are supported.
 */
export type ObjectLifecycleFilter = {
  /**
   * Exact key prefix; UTF-8 bytes are bounded by the portable key limit.
   */
  prefix?: string;
  tags?: Record<string, string>;
};

