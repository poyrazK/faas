/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Request to create a short-lived, one-time capability for one output artifact.
 */
export type CreateExecutionArtifactGrantRequest = {
  /**
   * Exact output artifact to grant.
   */
  artifact_name: string;
  /**
   * Grant lifetime. Defaults to five minutes.
   */
  expires_in_seconds?: number;
};

