/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Totals over returned retained records, not lifetime analytics. Completed occurrences include successful one-time schedules and recurring completion counts.
 */
export type ManagedRealtimeScheduleTotals = {
  pending: number;
  paused: number;
  published: number;
  failed: number;
  skipped: number;
  canceled: number;
  completed_occurrences: number;
  skipped_occurrences: number;
};

