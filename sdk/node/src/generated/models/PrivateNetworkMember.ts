/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Stable address reservation for one member of a Gregale-owned network.
 */
export type PrivateNetworkMember = {
  id: string;
  /**
   * Platform resource kind that owns the reservation, such as app.
   */
  owner_type: string;
  /**
   * Opaque platform resource identifier.
   */
  owner_id: string;
  address: string;
  created_at?: string;
};

