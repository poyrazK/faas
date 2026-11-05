/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Validated query inputs for an immutable-target recovery catalog read.
 */
export type ListEnvironmentGitReleasesRequest = {
  /**
   * Exact target-manifest revision against which recovery eligibility is assessed.
   */
  target_commit_sha: string;
  /**
   * Scoped anchor release from an earlier catalog response; omitted for the newest page.
   */
  before?: string;
};

