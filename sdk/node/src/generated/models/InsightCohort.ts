/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { InsightCohortRequest } from './InsightCohortRequest.js';
/**
 * A saved cohort and its last refresh.
 */
export type InsightCohort = (InsightCohortRequest & {
  /**
   * Customers in the cohort after the last refresh.
   */
  members: number;
  /**
   * More customers matched than max_members; the lowest customer IDs were kept.
   */
  truncated: boolean;
  /**
   * The plan's member bound.
   */
  max_members: number;
  refreshed_at?: string;
  refresh_requested_at: string;
  /**
   * Why the last refresh failed; the previous members are kept.
   */
  refresh_error?: string;
  created_by: string;
  created_at: string;
});

