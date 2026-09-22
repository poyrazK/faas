/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Last durable fabric and route convergence result for one compute node.
 */
export type AppPrivateNetworkNodeStatus = {
  /**
   * Compute-node identity that reported the observation.
   */
  node_id: string;
  fabric_status?: 'ready' | 'error';
  fabric_detail?: string;
  route_status?: 'ready' | 'error';
  route_detail?: string;
  observed_at?: string;
};

