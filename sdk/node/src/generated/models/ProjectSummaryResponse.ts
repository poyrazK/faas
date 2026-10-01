/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Stable account-scoped project list item.
 */
export type ProjectSummaryResponse = {
  /**
   * Immutable default compute region for new project workloads.
   */
  compute_region?: string;
  id: string;
  slug: string;
  repo_full_name?: string;
  production_branch?: string;
  scan_source: string;
  workload_count: number;
  created_at: string;
  updated_at: string;
};

