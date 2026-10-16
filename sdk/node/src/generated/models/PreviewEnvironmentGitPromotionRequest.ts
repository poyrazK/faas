/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Read-only environment promotion observation. A saved hash is not approval.
 */
export type PreviewEnvironmentGitPromotionRequest = {
  /**
   * Distinct source environment slug.
   */
  from_environment: string;
  /**
   * Verified commit containing the target environment manifest.
   */
  target_commit_sha: string;
  /**
   * Previously reviewed hash; changed source, release, artifact or target observations return 409.
   */
  expected_plan_hash?: string;
};

