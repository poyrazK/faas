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
};

