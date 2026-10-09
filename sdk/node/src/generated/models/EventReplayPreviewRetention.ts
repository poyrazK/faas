/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Existing receipt retention information without a complete archive guarantee.
 */
export type EventReplayPreviewRetention = {
  /**
   * Retention duration after routing settlement (2592000 seconds); unresolved receipts may survive longer.
   */
  settled_retention_seconds: number;
  /**
   * Account-wide earliest surviving acceptance, independent of target, range and filter; absent when none survive. Does not establish coverage.
   */
  earliest_retained_at?: string;
  /**
   * Always false because retained receipts are not a guaranteed complete archive.
   */
  history_complete: boolean;
};

