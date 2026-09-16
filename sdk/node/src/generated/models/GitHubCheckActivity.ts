/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type GitHubCheckActivity = {
  deployment_id: string;
  status: 'pending' | 'processing' | 'succeeded' | 'dead';
  commit_sha?: string;
  processed_at?: string | null;
  updated_at: string;
};

