/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { TenantHostnameResponse } from './TenantHostnameResponse.js';
/**
 * Observed routing and certificate readiness for one linked customer surface.
 */
export type PlatformTenantActivationSurfaceResponse = {
  id: string;
  app_id: string;
  name: string;
  status: string;
  cert_state: string;
  cert_not_after?: string;
  cert_last_error?: string;
  ready: boolean;
  hostnames: Array<TenantHostnameResponse>;
};

