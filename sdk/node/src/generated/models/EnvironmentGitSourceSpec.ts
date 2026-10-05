/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Pinned repository identity and source-of-truth controls for one environment.
 */
export type EnvironmentGitSourceSpec = {
  repository_id: number;
  installation_id: number;
  repository: string;
  ref: string;
  manifest_path: string;
  mode: 'report' | 'enforce';
  approval_policy: 'manual' | 'protected_branch';
  prune: boolean;
};

