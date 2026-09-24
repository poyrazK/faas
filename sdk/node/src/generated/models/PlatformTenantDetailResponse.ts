/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APIConsumerResponse } from './APIConsumerResponse.js';
import type { PlatformTenantSurfaceResponse } from './PlatformTenantSurfaceResponse.js';
/**
 * One customer and its current app-local consumer and surface links.
 */
export type PlatformTenantDetailResponse = {
  id: string;
  external_ref: string;
  name: string;
  status: 'active' | 'suspended';
  created_at: string;
  updated_at: string;
  consumers: Array<APIConsumerResponse>;
  surfaces: Array<PlatformTenantSurfaceResponse>;
};

