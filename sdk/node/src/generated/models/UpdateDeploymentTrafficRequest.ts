/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Body for PATCH /v1/deployments/{id}/traffic. Optionally require a particular live sibling to remain the sole 100% serving deployment when the update commits.
 */
export type UpdateDeploymentTrafficRequest = {
  /**
   * Per-deployment traffic-split weight. 0 = no traffic (used during rollback). 100 = sole live deployment.
   */
  traffic_percent: number;
  /**
   * Optional 32-hex or dashed deployment id. If this deployment is no longer the sole live 100% serving sibling at the transaction boundary, the update returns 409 traffic_serving_changed without changing traffic.
   */
  expected_serving_deployment_id?: string;
};

