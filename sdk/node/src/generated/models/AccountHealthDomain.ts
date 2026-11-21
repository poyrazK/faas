/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A custom domain that is not serving correctly.
 */
export type AccountHealthDomain = {
  /**
   * The custom hostname.
   */
  domain: string;
  /**
   * Slug of the app the hostname routes to.
   */
  app_slug?: string;
  /**
   * What is wrong with the hostname.
   */
  problem: string;
  /**
   * The certificate authority's last error, when issuance failed.
   */
  cert_error?: string;
};

