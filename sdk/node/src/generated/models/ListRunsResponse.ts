/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { JobRunResponse } from './JobRunResponse.js';
/**
 * Page of runs for one job. Cursor pagination uses
 * `?before=<run_id>` of the last row from the previous
 * page.
 *
 */
export type ListRunsResponse = {
  runs: Array<JobRunResponse>;
  /**
   * Cursor for the next page; empty/missing = end of page.
   */
  next_before?: string | null;
};

