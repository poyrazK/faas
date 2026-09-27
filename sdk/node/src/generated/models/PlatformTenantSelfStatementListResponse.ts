/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PlatformTenantStatementSummaryResponse } from './PlatformTenantStatementSummaryResponse.js';
/**
 * Bounded page of finalized statement summaries. next_offset is present only when another page exists.
 */
export type PlatformTenantSelfStatementListResponse = {
  statements: Array<PlatformTenantStatementSummaryResponse>;
  next_offset?: number;
};

