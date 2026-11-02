/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A wildcard custom domain's host allowlist. Names are the parts of hosts before the wildcard's suffix.
 */
export type CustomDomainHostsResponse = {
  domain: string;
  names: Array<string>;
  /**
   * False when no allowlist is set: every name below the wildcard may hold a certificate.
   */
  restricted: boolean;
};

