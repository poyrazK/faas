/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Selected active source deployment identity, without host paths or storage keys.
 */
export type EnvironmentGitPromotionArtifact = {
  /**
   * Logical workload resource.
   */
  resource: string;
  /**
   * Exact selected live deployment.
   */
  deployment_id: string;
  /**
   * Associated build identity, if present.
   */
  build_id?: string;
  /**
   * Recorded deployment kind.
   */
  kind: string;
  /**
   * Recorded immutable image identity, if present.
   */
  image_digest?: string;
  /**
   * Existing signed source-build producer identity.
   */
  build_artifact_id?: string;
  /**
   * Immutable producer input hash.
   */
  build_artifact_hash?: string;
  /**
   * Exact built application-layer byte digest.
   */
  artifact_digest?: string;
  /**
   * Original code commit, distinct from the target manifest commit.
   */
  build_commit_sha?: string;
};

