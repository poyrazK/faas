/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { BillingRunCounts } from './BillingRunCounts.js';
import type { BillingRunExport } from './BillingRunExport.js';
import type { BillingRunSources } from './BillingRunSources.js';
/**
 * A billing run and its progress. A run completes when no customer is pending; held and failed customers do not fail it.
 */
export type BillingRunResponse = {
  id: string;
  month: string;
  period_start: string;
  period_end: string;
  sources: BillingRunSources;
  finalize: boolean;
  export: BillingRunExport;
  hold_on_gaps: boolean;
  trigger: 'api' | 'schedule';
  status: 'queued' | 'running' | 'completed';
  counts: BillingRunCounts;
  created_at: string;
  started_at?: string;
  completed_at?: string;
};

