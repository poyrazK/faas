/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One environment branch in a list.
 */
export type ProjectEnvironmentBranchSummary = {
  slug: string;
  parent: string;
  branch_point: string;
  expires_at: string;
  egress: string;
  private_networks: 'off' | 'join';
  depth: number;
  data: 'ready' | 'pending' | 'failed';
  data_error?: string;
  created_at: string;
};

