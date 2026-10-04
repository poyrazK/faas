/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Request-driven global image release. Regional keys are never stored or returned.
 */
export type PutGlobalImageReleaseRequest = {
  image: string;
  /**
   * Regional account-admin keys indexed by region, supplied only for pending regions to resume.
   */
  regional_api_keys?: Record<string, string>;
};

