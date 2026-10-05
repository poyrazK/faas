/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Approve the reviewed digest for an immutable commit using the current source generation.
 */
export type ApproveEnvironmentGitRevisionRequest = {
  commit_sha: string;
  definition_digest: string;
  expected_generation: number;
};

