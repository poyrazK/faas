/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Existing receipt retention information without a complete archive guarantee.
 */
export type EventReplayPreviewRetention = {
  /**
   * The plan's retention duration after routing settlement (86400, 259200, 604800 or 1209600 seconds on Free, Hobby, Pro and Scale); unresolved receipts may survive longer.
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

