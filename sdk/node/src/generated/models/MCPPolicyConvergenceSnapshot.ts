/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Account-scoped live gateway replay observation for an active HTTP deployment. Snapshot metadata only; token enforcement is not attested. Correlates retained admission evidence when available.
 */
export type MCPPolicyConvergenceSnapshot = {
  state: 'active' | 'pending' | 'unverified' | 'paused' | 'unknown';
  captured_at: string;
  desired_revision: number;
  serving_gateways: number;
  pending_gateways: number;
  stale_gateways: number;
  /**
   * Distinct gateways with stale/missing/future reports or pending replay.
   */
  unconverged_gateways: number;
  deployment_id?: string;
  admission_id?: string;
  policy_status_path?: string;
  admission_history_path?: string;
};

