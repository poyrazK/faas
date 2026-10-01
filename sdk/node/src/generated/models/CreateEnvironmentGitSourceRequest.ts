/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Select a Git definition in the project repository; repository identities are verified by the server.
 */
export type CreateEnvironmentGitSourceRequest = {
  /**
   * Git ref selecting candidates; defaults to the project production branch.
   */
  ref?: string;
  manifest_path: string;
  mode?: 'report' | 'enforce';
  approval_policy?: 'manual' | 'protected_branch';
  prune?: boolean;
};

