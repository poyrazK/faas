/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Complete operator registry policy. UUID and region cannot change after registration.
 */
export type PutGlobalCellRequest = {
  region: string;
  /**
   * Canonical HTTPS API origin.
   */
  api_base: string;
  /**
   * Canonical regional apps DNS suffix. Omission preserves the registered suffix; an explicit empty string clears it. A change requires attachment verification again.
   */
  apps_domain?: string;
  enabled: boolean;
};

