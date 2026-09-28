/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { TenantHostnameResponse } from './TenantHostnameResponse.js';
/**
 * A tenant surface: a multi-hostname SAN bundle attached to one app. managed_by_platform_tenant is true only when created by a platform-tenant bundle apply.
 */
export type TenantSurfaceResponse = {
  id: string;
  account_id: string;
  app_id: string;
  name: string;
  cert_kind: 'per_host_san';
  status: 'pending' | 'active' | 'suspended' | 'deleted';
  cert_state: 'none' | 'pending' | 'issued' | 'renewing' | 'failed';
  cert_not_after?: string;
  cert_last_error?: string | null;
  created_at?: string;
  updated_at?: string;
  /**
   * True only when a platform-tenant bundle created this surface; omitted otherwise.
   */
  managed_by_platform_tenant?: boolean;
  hostnames: Array<TenantHostnameResponse>;
};

