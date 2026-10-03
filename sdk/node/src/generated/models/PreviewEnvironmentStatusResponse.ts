/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PreviewEnvironmentMemberResponse } from './PreviewEnvironmentMemberResponse.js';
/**
 * Aggregate current-head readiness for a recorded GitHub PR preview workload set.
 */
export type PreviewEnvironmentStatusResponse = {
  root_slug: string;
  repo_full_name: string;
  pr_number: number;
  commit_sha: string;
  phase: 'building' | 'live' | 'failed' | 'closed';
  ready: boolean;
  summary: string;
  live_workloads: number;
  total_workloads: number;
  members: Array<PreviewEnvironmentMemberResponse>;
};

