/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ApplyPlatformTenantConsumerRequest } from './ApplyPlatformTenantConsumerRequest.js';
/**
 * Additive, retry-safe onboarding; dry_run validates and previews without writes.
 */
export type ApplyPlatformTenantRequest = {
  external_ref: string;
  name: string;
  dry_run?: boolean;
  consumers?: Array<ApplyPlatformTenantConsumerRequest>;
  surface_ids?: Array<string>;
};

