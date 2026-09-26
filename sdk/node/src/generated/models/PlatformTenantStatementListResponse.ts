/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PlatformTenantStatementResponse } from './PlatformTenantStatementResponse.js';
/**
 * Immutable statement revisions for one tenant and period.
 */
export type PlatformTenantStatementListResponse = {
  statements: Array<PlatformTenantStatementResponse>;
  next_offset?: number;
};

