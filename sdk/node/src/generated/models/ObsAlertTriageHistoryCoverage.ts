/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Retained receipt counts across revisions one through snapshot_revision independent of page truncation.
 */
export type ObsAlertTriageHistoryCoverage = {
  state: 'complete' | 'gaps';
  reason?: 'missing_receipts';
  available_entries: number;
  missing_entries: number;
  oldest_available_revision?: number;
  newest_available_revision?: number;
};

