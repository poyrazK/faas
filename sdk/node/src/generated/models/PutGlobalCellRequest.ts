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
  enabled: boolean;
};

