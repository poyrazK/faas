/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Revision-guarded replacement lifecycle alert policy.
 */
export type UpdateIssueLifecycleAlertPolicyRequest = {
  expected_revision: number;
  /**
   * Replacement allowlist of exact names; an empty array removes the environment filter.
   */
  environments: Array<string>;
  cooldown_seconds: number;
};

