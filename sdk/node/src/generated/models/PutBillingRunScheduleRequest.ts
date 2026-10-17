/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { BillingRunExport } from './BillingRunExport.js';
import type { BillingRunSources } from './BillingRunSources.js';
/**
 * A monthly billing run schedule. Options default like a run's; delay_hours defaults to 24.
 */
export type PutBillingRunScheduleRequest = {
  sources?: BillingRunSources;
  /**
   * Finalize each drafted statement in scheduled runs (default true).
   */
  finalize?: boolean;
  export?: BillingRunExport;
  /**
   * Hold app consumers with completeness gaps in scheduled runs (default true).
   */
  hold_on_gaps?: boolean;
  /**
   * Hours after month end before the run starts (default 24).
   */
  delay_hours?: number;
};

