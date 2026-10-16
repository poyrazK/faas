/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Observed issue and occurrence counts for one deployment within the requested environment.
 */
export type IssueReleaseHealth = {
  deployment_id: string;
  commit_sha: string;
  created_at: string;
  observed_requests: number;
  http_5xx_requests: number;
  http_5xx_rate: number | null;
  request_coverage: 'observed' | 'unknown';
  observed_issue_events: number;
  observed_issue_groups: number;
  new_issue_groups: number;
  regressed_issue_groups: number;
  identified_customers: number;
  unattributed_events: number;
};

