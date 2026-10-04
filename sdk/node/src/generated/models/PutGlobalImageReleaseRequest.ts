/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Global image release with sealed scoped consent and background recovery. Regional admin keys are never stored or returned.
 */
export type PutGlobalImageReleaseRequest = {
  image: string;
  /**
   * Request-only regional account-admin keys indexed by region, supplied to establish or renew scoped consent. Active consent supports recovery and later releases without these keys.
   */
  regional_api_keys?: Record<string, string>;
};

