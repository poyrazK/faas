/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Select a Git definition. GitHub uses the project's bound repository; GitLab.com requires an account OAuth connection, a repository path and an explicit ref. Repository identity is verified by the server. Both providers support manual or verified protected-branch approval. Production enforcement remains unavailable in this preview.
 */
export type CreateEnvironmentGitSourceRequest = {
  provider?: 'github' | 'gitlab';
  /**
   * Canonical GitLab.com group/project path, including subgroups when present.
   */
  repository?: string;
  /**
   * Git ref selecting candidates; GitHub defaults to the project production branch.
   */
  ref?: string;
  manifest_path: string;
  mode?: 'report';
  approval_policy?: 'manual' | 'protected_branch';
  prune?: boolean;
};

