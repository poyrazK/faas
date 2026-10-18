/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Versioned contained cold-start budget for every declared workload. Requires an operator-qualified cold-start fleet and excludes worker termination.
 */
export type ScenarioColdStartSpec = {
  version: 1;
  /**
   * Maximum host-observed startup duration through guest readiness.
   */
  within_ms: number;
};

