/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Protected branch policy and approval checks bound to the exact environment definition commit.
 */
export type EnvironmentGitProtectedBranchEvidence = {
  qualified: boolean;
  /**
   * Protected branch check failure reason; excluded from durable approval evidence.
   */
  reason?: string;
  profile: string;
  installation_id: number;
  repository_id: number;
  repository: string;
  branch: string;
  commit_sha: string;
  policy_digest: string;
  required_review_count: number;
  checked_at: string;
};

