/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A recorded change that sent all traffic in an environment to a deployment.
 */
export type AlertAnomalyDeploymentPromotion = {
  deployment_id: string;
  /**
   * Upstream commit SHA when recorded.
   */
  commit_sha?: string;
  /**
   * When a traffic change last took this deployment to 100 percent of the scope.
   */
  promoted_at: string;
};

