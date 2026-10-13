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
};

