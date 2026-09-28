/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ApplyPlatformTenantConsumerRequest } from './ApplyPlatformTenantConsumerRequest.js';
import type { ApplyPlatformTenantSurfaceRequest } from './ApplyPlatformTenantSurfaceRequest.js';
/**
 * Desired bundle and plan digest returned by the read-only reconciliation-plan endpoint.
 */
export type ApplyPlatformTenantReconciliationRequest = {
  consumers?: Array<ApplyPlatformTenantConsumerRequest>;
  surface_ids?: Array<string>;
  surfaces?: Array<ApplyPlatformTenantSurfaceRequest>;
  expected_plan_hash: string;
};

