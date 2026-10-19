/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { BillingRunExport } from './BillingRunExport.js';
import type { BillingRunSources } from './BillingRunSources.js';
/**
 * The account's billing run schedule and the month it bills next.
 */
export type BillingRunScheduleResponse = {
  sources: BillingRunSources;
  finalize: boolean;
  export: BillingRunExport;
  hold_on_gaps: boolean;
  delay_hours: number;
  next_month: string;
  next_run_at: string;
  /**
   * The last month the schedule started a run for.
   */
  last_month?: string;
  created_at: string;
  updated_at: string;
};

