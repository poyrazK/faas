/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RuntimePolicyComponentStatus } from './RuntimePolicyComponentStatus.js';
/**
 * Fresh serving-gateway status for app-cache and deployment traffic changes. The edge_rules component reports its separate revision sequence.
 */
export type RuntimePolicyStatusResponse = {
  app_id: string;
  /**
   * Latest desired control-plane revision for app-cache and deployment traffic policy.
   */
  desired_revision: number;
  state: 'active' | 'pending' | 'unverified';
  coverage: Array<string>;
  serving_gateways: number;
  applied_gateways: number;
  pending_gateways: number;
  stale_gateways: number;
  edge_rules: RuntimePolicyComponentStatus;
};

