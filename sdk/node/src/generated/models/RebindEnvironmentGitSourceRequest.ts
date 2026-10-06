/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Replace a Git binding with a fresh report-only source that requires a new approval.
 */
export type RebindEnvironmentGitSourceRequest = {
  expected_generation: number;
  ref?: string;
  manifest_path: string;
  approval_policy?: 'manual' | 'protected_branch';
};

