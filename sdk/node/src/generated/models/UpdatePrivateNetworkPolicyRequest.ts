/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * PUT /v1/networks/{id}/policy body.
 */
export type UpdatePrivateNetworkPolicyRequest = {
  /**
   * CIDRs reachable by attached workloads; empty disables the policy.
   */
  allowed_cidrs: Array<string>;
};

