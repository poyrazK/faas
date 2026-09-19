/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * POST /v1/networks/{id}/peerings body.
 */
export type CreatePrivateNetworkPeeringRequest = {
  /**
   * Another Gregale-owned network in the same account and region.
   */
  peer_network_id: string;
};

