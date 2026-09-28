/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ApplyPlatformTenantHostnameResponse } from './ApplyPlatformTenantHostnameResponse.js';
/**
 * Planned or applied tenant surface, hostname challenges, and current certificate state.
 */
export type ApplyPlatformTenantSurfaceResponse = {
  /**
   * Absent when a dry run would create the surface.
   */
  id?: string;
  app_id: string;
  name: string;
  status: 'pending' | 'active' | 'suspended';
  cert_state: 'none' | 'pending' | 'issued' | 'failed';
  action: 'create' | 'link' | 'unchanged';
  /**
   * True only after the tenant bundle created this surface; omitted for unmanaged resources and dry-run creates. Linking an existing surface does not adopt it.
   */
  managed_by_platform_tenant?: boolean;
  hostnames?: Array<ApplyPlatformTenantHostnameResponse>;
};

