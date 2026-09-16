/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Request to approve one exact plan for a protected environment. Provide exactly one of plan_token or promotion_token.
 */
export type CreateProjectEnvironmentApprovalRequest = {
  /**
   * Exact plan token returned by the scan endpoint.
   */
  plan_token?: string;
  /**
   * Exact promotion token returned by the environment promotion preview.
   */
  promotion_token?: string;
};

