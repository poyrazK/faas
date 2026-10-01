/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EnvironmentGitProtectedBranchEvidence } from './EnvironmentGitProtectedBranchEvidence.js';
import type { EnvironmentGitReviewEvidence } from './EnvironmentGitReviewEvidence.js';
/**
 * Reviews of the final PR head before merge. Permissions and protection describe the check time, not historical policy.
 */
export type EnvironmentGitReviewedMergeEvidence = {
  reviewed_definition_digest: string;
  qualified: boolean;
  profile: string;
  policy: EnvironmentGitProtectedBranchEvidence;
  pull_request_id: number;
  pull_request_number: number;
  author_id: number;
  head_sha: string;
  merged_at: string;
  reviews: Array<EnvironmentGitReviewEvidence>;
  checked_at: string;
};

