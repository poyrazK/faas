/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { BillingRunExport } from './BillingRunExport.js';
import type { BillingRunSources } from './BillingRunSources.js';
/**
 * A month-end billing run (ADR-1503). Omitted options bill both sources, finalize, do not export, and hold consumers with completeness gaps.
 */
export type CreateBillingRunRequest = {
  /**
   * UTC calendar month to bill, YYYY-MM; it must have ended at least an hour ago.
   */
  month: string;
  sources?: BillingRunSources;
  /**
   * Finalize each drafted statement (default true).
   */
  finalize?: boolean;
  export?: BillingRunExport;
  /**
   * Hold an app consumer whose usage ledger misses requests telemetry saw (default true).
   */
  hold_on_gaps?: boolean;
};

