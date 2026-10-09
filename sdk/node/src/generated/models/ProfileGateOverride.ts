/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Audited customer exception bound to the current profiling policy revision.
 */
export type ProfileGateOverride = {
  expected_policy_revision: number;
  /**
   * Nonblank customer reason recorded atomically with the traffic change. Workers cannot override.
   */
  reason: string;
};

