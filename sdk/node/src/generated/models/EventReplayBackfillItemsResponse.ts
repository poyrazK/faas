/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventReplayBackfillItemResponse } from './EventReplayBackfillItemResponse.js';
/**
 * Stable acceptance-ordered page of per-envelope outcomes for one backfill job.
 */
export type EventReplayBackfillItemsResponse = {
  job_id: string;
  items: Array<EventReplayBackfillItemResponse>;
  /**
   * Continue with the same job and state filter; absent when the page is final.
   */
  next_after?: string;
};

