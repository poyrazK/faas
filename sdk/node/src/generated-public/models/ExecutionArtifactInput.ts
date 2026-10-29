/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One same-family artifact reference or a one-time cross-agent grant, staged at a normalized path in the next run's ephemeral bundle.
 */
export type ExecutionArtifactInput = {
  /**
   * ID of a successful execution owned by this key family (omit when using grant_token).
   */
  execution_id?: string;
  /**
   * Exact artifact name from the source execution receipt (omit when using grant_token).
   */
  name?: string;
  /**
   * Single-use token created for one artifact by another Runs key family.
   */
  grant_token?: string;
  /**
   * Normalized relative destination path in the new execution bundle.
   */
  path: string;
};

