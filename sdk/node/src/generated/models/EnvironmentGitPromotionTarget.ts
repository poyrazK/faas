/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Target binding and manifest observation; no target values are returned.
 */
export type EnvironmentGitPromotionTarget = {
  /**
   * Target source binding identity.
   */
  source_id: string;
  /**
   * Target source generation.
   */
  generation: number;
  /**
   * Observed target intent version.
   */
  intent_version: number;
  /**
   * Immutable target manifest commit.
   */
  commit_sha: string;
  /**
   * Required local base definition digest.
   */
  definition_digest: string;
  /**
   * Digest after replacing only proposed image source contracts.
   */
  proposed_digest: string;
  /**
   * Bound target manifest path in Git.
   */
  manifest_path: string;
};

