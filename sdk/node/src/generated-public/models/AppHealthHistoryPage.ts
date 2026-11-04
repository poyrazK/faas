/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppHealthHistoryEntry } from './AppHealthHistoryEntry.js';
import type { AppHealthResponse } from './AppHealthResponse.js';
/**
 * Retained default HTTP health observations with independent background collection freshness.
 */
export type AppHealthHistoryPage = {
  app_id: string;
  scope: 'default';
  entries: Array<AppHealthHistoryEntry>;
  next_cursor?: string;
  /**
   * Latest background assessment with its original timestamp; may be stale.
   */
  latest?: AppHealthResponse;
  /**
   * Latest background assessment exists and has not expired. This is collection freshness, not app health.
   */
  collector_fresh: boolean;
  /**
   * Minimum target spacing per app; fleet load and failures can delay observations.
   */
  interval_seconds: number;
};

