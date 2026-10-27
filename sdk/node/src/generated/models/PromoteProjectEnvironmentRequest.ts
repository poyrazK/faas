/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Request to execute one exact environment promotion.
 */
export type PromoteProjectEnvironmentRequest = {
  from_environment: string;
  /**
   * Exact promotion token returned by the preview endpoint.
   */
  promotion_token: string;
  /**
   * Short-lived approval for a protected target environment.
   */
  approval_token?: string;
  /**
   * Promote into production while a deploy freeze covers one of the apps; the reason is audited.
   */
  override_freeze?: string;
};

