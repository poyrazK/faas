/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Read-only environment rollback observation. A saved hash is not approval.
 */
export type PreviewEnvironmentGitRollbackRequest = {
  /**
   * Retained GitOps serving release UUID in this environment.
   */
  release_set_id: string;
  /**
   * Immutable commit whose current manifest will receive the rollback source changes.
   */
  target_commit_sha: string;
  /**
   * Previously reviewed rollback hash; changed historical evidence or target intent returns 409.
   */
  expected_plan_hash?: string;
};

