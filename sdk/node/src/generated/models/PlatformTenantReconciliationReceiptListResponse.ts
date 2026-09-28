/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PlatformTenantReconciliationReceiptSummary } from './PlatformTenantReconciliationReceiptSummary.js';
/**
 * One page of reconciliation receipt summaries and an optional cursor for older receipts.
 */
export type PlatformTenantReconciliationReceiptListResponse = {
  receipts: Array<PlatformTenantReconciliationReceiptSummary>;
  /**
   * Opaque cursor for the next page.
   */
  next_page_token?: string;
};

