/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Fresh serving-gateway application status for app-cache and deployment traffic changes.
 */
export type RuntimePolicyStatusResponse = {
  app_id: string;
  desired_revision: number;
  state: 'active' | 'pending' | 'unverified';
  coverage: Array<string>;
  serving_gateways: number;
  applied_gateways: number;
  pending_gateways: number;
  stale_gateways: number;
};

