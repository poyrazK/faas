/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * PUT body for /v1/apps/{slug}/network/private.
 */
export type AppPrivateNetworkAttachmentRequest = {
  network_id: string;
  /**
   * Optional when attaching a Gregale-owned network; it must match the network region.
   */
  region?: string;
  cidrs?: Array<string>;
  /**
   * Optional private-network policy ranges. Each range must be contained by the attached network CIDR.
   */
  allowed_cidrs?: Array<string>;
};

