/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Current live deployment metadata for one project workload.
 */
export type ProjectEnvironmentReleaseWorkloadResponse = {
  workload_slug: string;
  workload_name: string;
  status: 'live' | 'not_deployed';
  deployment_id?: string;
  build_id?: string;
  image_digest?: string;
  source_url?: string;
  commit_sha?: string;
  source_sha256?: string;
  traffic_percent?: number;
  created_at?: string;
};

