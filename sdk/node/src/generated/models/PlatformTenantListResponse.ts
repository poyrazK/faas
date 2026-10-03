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
  /**
   * Opaque cursor for the next stable page; omitted when no more tenants remain.
   */
  next_page_token?: string;
};

