/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ApplyPlatformTenantConsumerRequest } from './ApplyPlatformTenantConsumerRequest.js';
import type { ApplyPlatformTenantSurfaceRequest } from './ApplyPlatformTenantSurfaceRequest.js';
/**
 * Complete desired consumer and surface bundle for one existing platform tenant. Omitted managed resources appear as removal candidates; omitted unmanaged resources are explicitly retained.
 */
export type PlanPlatformTenantReconciliationRequest = {
  consumers?: Array<ApplyPlatformTenantConsumerRequest>;
  surface_ids?: Array<string>;
  surfaces?: Array<ApplyPlatformTenantSurfaceRequest>;
};

