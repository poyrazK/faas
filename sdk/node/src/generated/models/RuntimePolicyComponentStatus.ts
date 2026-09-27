/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Fresh serving-gateway status for a policy component with explicit scope. Its desired revision is meaningful within that component's ledger or filtered projection.
 */
export type RuntimePolicyComponentStatus = {
  scope?: 'app' | 'account';
  desired_revision: number;
  state: 'active' | 'pending' | 'unverified';
  serving_gateways: number;
  applied_gateways: number;
  pending_gateways: number;
  stale_gateways: number;
};

