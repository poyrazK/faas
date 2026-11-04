/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Application deployment and binding selection to review without admitting work.
 */
export type ReviewBindingVerificationRequest = {
  /**
   * Exact deployment; omission selects the current live deployment once.
   */
  deployment_id?: string;
  service_targets?: Record<string, string>;
};

