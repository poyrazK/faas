/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PlatformTenantSelfActivationSurfaceResponse } from './PlatformTenantSelfActivationSurfaceResponse.js';
/**
 * Current read-only activation snapshot for the tenant bound to the bearer; ready requires every linked surface to be ready.
 */
export type PlatformTenantSelfActivationResponse = {
  status: 'active' | 'suspended';
  enabled: boolean;
  ready: boolean;
  surfaces: Array<PlatformTenantSelfActivationSurfaceResponse>;
};

