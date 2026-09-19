/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Durable, provider-neutral peering intent between two Gregale-owned networks.
 */
export type PrivateNetworkPeering = {
  id: string;
  network_id: string;
  peer_network_id: string;
  region: string;
  status: 'pending' | 'ready' | 'error';
  status_detail?: string;
  created_at?: string;
  updated_at?: string;
};

