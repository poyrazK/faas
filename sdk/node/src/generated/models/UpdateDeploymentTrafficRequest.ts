/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppReleaseReview } from './AppReleaseReview.js';
/**
 * Body for PATCH /v1/deployments/{id}/traffic. Optionally require a particular live sibling to remain the sole 100% serving deployment when the update commits.
 */
export type UpdateDeploymentTrafficRequest = {
  /**
   * Atomically require this named-environment review at promotion. Requires 100% traffic and expected_serving_deployment_id; protected targets cannot use manual promotion.
   */
  expected_review?: AppReleaseReview;
  /**
   * Per-deployment traffic-split weight. 0 = no traffic (used during rollback). 100 = sole live deployment.
   */
  traffic_percent: number;
  /**
   * Optional 32-hex or dashed deployment id. An explicit empty string requires no live sibling with positive traffic, for guarded first deployment promotion. If the condition changes at the transaction boundary, the update returns 409 traffic_serving_changed without changing traffic.
   */
  expected_serving_deployment_id?: string;
  /**
   * Optional account-wide stored MCP policy condition, checked in the traffic transaction. Changed overlapping JWT rules return 409 mcp_access_policy_changed without changing traffic. When account_revision is supplied, the transaction also requires the unchanged account revision and fresh replay from every currently registered serving gateway. Gateway evidence is a database snapshot at admission, not runtime authorization or a lease on fleet membership.
   */
  expected_mcp_access_policy?: {
    /**
     * Reviewed HTTPS MCP endpoint without credentials, query or fragment.
     */
    resource: string;
    /**
     * Optional audited account edge-rule revision. Returns 409 mcp_account_revision_changed, mcp_gateway_evidence_unavailable, mcp_gateway_evidence_stale, or mcp_gateway_replay_pending when the transaction cannot admit promotion.
     */
    account_revision?: number;
    /**
     * Version 1 SHA-256 fingerprint returned by gregale mcp access audit.
     */
    fingerprint: string;
  };
};

