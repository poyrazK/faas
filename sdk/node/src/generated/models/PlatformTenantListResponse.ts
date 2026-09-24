/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PlatformTenantResponse } from './PlatformTenantResponse.js';
/**
 * A bounded page of account-level customers.
 */
export type PlatformTenantListResponse = {
  tenants: Array<PlatformTenantResponse>;
  next_offset?: number;
};

