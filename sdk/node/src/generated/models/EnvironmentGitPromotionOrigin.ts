/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Scoped identity of the approved source release; graph and release IDs are empty if unproven.
 */
export type EnvironmentGitPromotionOrigin = {
  /**
   * Source binding identity.
   */
  source_id: string;
  /**
   * Current approved revision identity.
   */
  revision_id: string;
  /**
   * Approved immutable source commit.
   */
  commit_sha: string;
  /**
   * Approved source definition digest.
   */
  definition_digest: string;
  /**
   * Source approval generation.
   */
  generation: number;
  /**
   * Observed source intent version.
   */
  intent_version: number;
  /**
   * Current qualified graph identity, if present.
   */
  graph_id: string;
  /**
   * Active source release identity, if present.
   */
  release_set_id: string;
  /**
   * Bound provider.
   */
  provider: 'github' | 'gitlab';
  /**
   * Immutable provider repository identity.
   */
  repository_id: number;
};

