/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Immutable deployment metadata and aggregate observations for one issue.
 */
export type IssueRelease = {
  deployment_id: string;
  commit_sha?: string;
  image_digest?: string;
  event_count: number;
  first_seen_at: string;
  last_seen_at: string;
};

