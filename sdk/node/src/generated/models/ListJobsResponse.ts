/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { JobResponse } from './JobResponse.js';
/**
 * Page of jobs. Pass the LAST id of the returned slice as
 * the next `?before=` to load older jobs. `quota_max` and
 * `count` are echoed for dashboard rendering (3/25 jobs
 * progress bar).
 *
 */
export type ListJobsResponse = {
  jobs: Array<JobResponse>;
  /**
   * Cursor for the next page; empty/missing = end of page.
   */
  next_before?: string | null;
  /**
   * Per-plan cap (JobMaxPerAccount).
   */
  quota_max: number;
  /**
   * Number of jobs returned in this page.
   */
  count: number;
};

