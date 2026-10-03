/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PlatformTenantActivationSurfaceResponse } from './PlatformTenantActivationSurfaceResponse.js';
/**
 * Account customer activation snapshot; ready is false until all linked hostname surfaces are serving.
 */
export type PlatformTenantActivationResponse = {
  tenant_id: string;
  status: 'active' | 'suspended';
  enabled: boolean;
  ready: boolean;
  surfaces: Array<PlatformTenantActivationSurfaceResponse>;
};

