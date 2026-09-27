/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PlatformTenantSelfActivationHostnameResponse } from './PlatformTenantSelfActivationHostnameResponse.js';
import type { PlatformTenantSelfDeploymentResponse } from './PlatformTenantSelfDeploymentResponse.js';
/**
 * Redacted state for one surface linked to the caller's platform tenant.
 */
export type PlatformTenantSelfActivationSurfaceResponse = {
  id: string;
  name: string;
  status: 'pending' | 'active' | 'suspended';
  cert_state: 'none' | 'pending' | 'issued' | 'failed';
  cert_not_after?: string;
  ready: boolean;
  latest_deployment?: PlatformTenantSelfDeploymentResponse;
  hostnames: Array<PlatformTenantSelfActivationHostnameResponse>;
};

