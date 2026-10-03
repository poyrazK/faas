/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ApplyPlatformTenantConsumerResponse } from './ApplyPlatformTenantConsumerResponse.js';
import type { ApplyPlatformTenantSurfaceResponse } from './ApplyPlatformTenantSurfaceResponse.js';
/**
 * Tenant onboarding plan or applied result; omitted IDs in a dry run would be created.
 */
export type ApplyPlatformTenantResponse = {
  /**
   * Absent when a dry run would create this tenant.
   */
  tenant_id?: string;
  external_ref: string;
  name: string;
  status: 'active' | 'suspended';
  action: 'create' | 'unchanged';
  dry_run: boolean;
  consumers: Array<ApplyPlatformTenantConsumerResponse>;
  surfaces: Array<ApplyPlatformTenantSurfaceResponse>;
};

