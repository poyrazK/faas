/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Replaces a wildcard custom domain's host allowlist; DELETE the allowlist to allow every name again.
 */
export type SetCustomDomainHostsRequest = {
  /**
   * Names below the wildcard (or full hosts) that may hold certificates.
   */
  names: Array<string>;
};

