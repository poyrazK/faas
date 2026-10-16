/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Purge intent plus fresh current-process replay evidence for the registered compute ingress fleet.
 */
export type EnvironmentCachePurgeStatus = {
  app_id: string;
  project_id: string;
  environment_id: string;
  scope: string;
  request_id: string;
  path: string;
  generation: number;
  requested_at: string;
  status: 'completed' | 'pending' | 'degraded';
  checked_at: string;
  serving_gateways: number;
  acknowledged_gateways: number;
  pending_gateways: number;
  stale_gateways: number;
};

