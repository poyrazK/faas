/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Session-bound gateway acknowledgements for one exact environment cache policy revision.
 */
export type EnvironmentCacheConvergence = {
  app_id: string;
  project_id: string;
  environment_id: string;
  scope: string;
  revision: number;
  status: 'pending' | 'converged' | 'degraded';
  checked_at: string;
  serving_gateways: number;
  acknowledged_gateways: number;
  pending_gateways: number;
  stale_gateways: number;
};

