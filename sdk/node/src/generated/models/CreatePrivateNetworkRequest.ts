/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * POST /v1/networks body for a Gregale-owned network.
 */
export type CreatePrivateNetworkRequest = {
  name: string;
  region: string;
  cidr: string;
  /**
   * Optional reusable CIDR allowlist contained by cidr.
   */
  allowed_cidrs?: Array<string>;
};

