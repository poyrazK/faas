/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PlatformTenantOffboardingReceiptSummary } from './PlatformTenantOffboardingReceiptSummary.js';
/**
 * One page of offboarding receipt summaries and an optional cursor for older entries.
 */
export type PlatformTenantOffboardingReceiptListResponse = {
  receipts: Array<PlatformTenantOffboardingReceiptSummary>;
  next_page_token?: string;
};

