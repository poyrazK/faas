/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Read-only publication status, manifest digest and retained map count for one deployment.
 */
export type IssueSourceMapsMetadata = {
  deployment_id: string;
  manifest_sha256: string;
  map_count: number;
  ready: boolean;
  created_at: string;
  retired_at?: string;
};

