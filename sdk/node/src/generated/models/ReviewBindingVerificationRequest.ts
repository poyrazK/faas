/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type ReviewBindingVerificationRequest = {
  /**
   * Exact deployment; omission selects the current live deployment once.
   */
  deployment_id?: string;
  service_targets?: Record<string, string>;
};

