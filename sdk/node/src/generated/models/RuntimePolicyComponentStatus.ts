/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Fresh serving-gateway application status for a policy ledger with its own revision sequence.
 */
export type RuntimePolicyComponentStatus = {
  desired_revision: number;
  state: 'active' | 'pending' | 'unverified';
  serving_gateways: number;
  applied_gateways: number;
  pending_gateways: number;
  stale_gateways: number;
};

