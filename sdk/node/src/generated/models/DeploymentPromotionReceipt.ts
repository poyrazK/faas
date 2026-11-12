/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Immutable acceptance of an original guarded 100 percent traffic promotion; current traffic and health are separate.
 */
export type DeploymentPromotionReceipt = {
  attempt_id: string;
  request_hash: string;
  app_id: string;
  deployment_id: string;
  /**
   * Original serving guard; empty requires no serving sibling.
   */
  expected_serving_deployment_id: string;
  scope: string;
  project_id?: string;
  environment_id?: string;
  accepted_at: string;
};

